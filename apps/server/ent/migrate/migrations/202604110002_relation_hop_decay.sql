ALTER TABLE system_settings
  ADD COLUMN IF NOT EXISTS memory_relation_max_hops INTEGER,
  ADD COLUMN IF NOT EXISTS memory_relation_hop_decay_lambda DOUBLE PRECISION,
  ADD COLUMN IF NOT EXISTS memory_relation_hop_decay_exponent DOUBLE PRECISION;
