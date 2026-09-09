-- Backfill only unambiguous legacy edges. A generationless Key Memory endpoint
-- can inherit its scoped neighbour's generation, in either direction. Preserve
-- unresolved/conflicting edges for inspection or rebuild; online scoped reads
-- exclude them instead of deleting historical graph data during migration.
UPDATE memory_relations relation
SET generation_id = COALESCE(NULLIF(src.generation_id, ''), dst.generation_id),
    updated_at = NOW()
FROM memory_nodes src, memory_nodes dst
WHERE relation.generation_id = ''
  AND src.user_id = relation.user_id AND src.id = relation.src_node_id
  AND dst.user_id = relation.user_id AND dst.id = relation.dst_node_id
  AND (src.generation_id <> '' OR dst.generation_id <> '')
  AND (src.generation_id = '' OR dst.generation_id = '' OR src.generation_id = dst.generation_id);
