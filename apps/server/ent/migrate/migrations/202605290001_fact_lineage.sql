ALTER TABLE facts
  ADD COLUMN IF NOT EXISTS fact_key TEXT,
  ADD COLUMN IF NOT EXISTS lineage_group_id TEXT,
  ADD COLUMN IF NOT EXISTS revision_seq INTEGER NOT NULL DEFAULT 1;

CREATE INDEX IF NOT EXISTS idx_facts_lineage_group
  ON facts (user_id, generation_id, lineage_group_id, revision_seq);

CREATE INDEX IF NOT EXISTS idx_facts_current_head
  ON facts (user_id, generation_id, lineage_group_id, updated_at DESC)
  WHERE is_superseded = FALSE AND superseded_by_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_keywords_user_summary
  ON memory_keywords (user_id, summary_id, keyword);