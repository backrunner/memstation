ALTER TABLE facts
  ADD COLUMN IF NOT EXISTS fact_key TEXT,
  ADD COLUMN IF NOT EXISTS lineage_group_id TEXT,
  ADD COLUMN IF NOT EXISTS revision_seq INTEGER NOT NULL DEFAULT 1;

UPDATE facts older
SET fact_key = COALESCE(
    NULLIF(older.fact_key, ''),
    NULLIF(newer.fact_key, ''),
    newer.id,
    older.id
  )
FROM facts newer
WHERE older.user_id = newer.user_id
  AND older.generation_id = newer.generation_id
  AND older.superseded_by_id = newer.id
  AND (older.fact_key IS NULL OR older.fact_key = '');

UPDATE facts
SET fact_key = id
WHERE fact_key IS NULL OR fact_key = '';

UPDATE facts
SET lineage_group_id = 'fact-lineage:' || user_id || ':' || COALESCE(NULLIF(trim(both '_' from regexp_replace(lower(COALESCE(NULLIF(fact_key, ''), id)), '[^a-z0-9_-]+', '_', 'g')), ''), 'unknown')
WHERE fact_key IS NOT NULL AND fact_key <> '';

UPDATE facts
SET lineage_group_id = COALESCE(
    NULLIF(lineage_group_id, ''),
    'fact-lineage:' || user_id || ':' || COALESCE(NULLIF(trim(both '_' from regexp_replace(lower(id), '[^a-z0-9_-]+', '_', 'g')), ''), 'unknown')
  )
WHERE lineage_group_id IS NULL OR lineage_group_id = '';

WITH ordered AS (
  SELECT id,
         ROW_NUMBER() OVER (
           PARTITION BY user_id, generation_id, lineage_group_id
           ORDER BY created_at ASC, updated_at ASC, id ASC
         ) AS next_revision_seq
  FROM facts
)
UPDATE facts f
SET revision_seq = ordered.next_revision_seq
FROM ordered
WHERE f.id = ordered.id
  AND (f.revision_seq IS NULL OR f.revision_seq <= 1);

CREATE INDEX IF NOT EXISTS idx_facts_lineage_group
  ON facts (user_id, generation_id, lineage_group_id, revision_seq);

CREATE INDEX IF NOT EXISTS idx_facts_current_head
  ON facts (user_id, generation_id, lineage_group_id, updated_at DESC)
  WHERE is_superseded = FALSE AND superseded_by_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_keywords_user_summary
  ON memory_keywords (user_id, summary_id, keyword);

INSERT INTO memory_relations (
  id, generation_id, user_id, src_node_id, dst_node_id, relation_type,
  weight, source, metadata, created_at, updated_at
)
SELECT
  'fact-supersedes:' || md5(f.user_id || ':' || newer.node_id || ':' || f.node_id),
  f.generation_id,
  f.user_id,
  newer.node_id,
  f.node_id,
  'supersedes',
  1,
  'fact_lineage_backfill',
  jsonb_build_object(
    'superseded_fact_id', f.id,
    'superseding_fact_id', newer.id,
    'fact_key', f.fact_key,
    'lineage_group_id', f.lineage_group_id
  ),
  COALESCE(f.valid_to, f.updated_at, NOW()),
  NOW()
FROM facts f
JOIN facts newer
  ON newer.user_id = f.user_id
 AND newer.generation_id = f.generation_id
 AND newer.id = f.superseded_by_id
WHERE f.superseded_by_id IS NOT NULL
  AND f.superseded_by_id <> ''
  AND f.id <> newer.id
  AND f.node_id <> ''
  AND newer.node_id <> ''
ON CONFLICT (user_id, src_node_id, dst_node_id, relation_type) DO UPDATE SET
  generation_id = EXCLUDED.generation_id,
  weight = EXCLUDED.weight,
  source = EXCLUDED.source,
  metadata = EXCLUDED.metadata,
  updated_at = EXCLUDED.updated_at;
