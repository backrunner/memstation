ALTER TABLE system_settings
  ADD COLUMN IF NOT EXISTS rerank_model TEXT,
  ADD COLUMN IF NOT EXISTS rerank_candidate_multiplier INTEGER,
  ADD COLUMN IF NOT EXISTS rerank_min_candidates INTEGER,
  ADD COLUMN IF NOT EXISTS rerank_max_candidates INTEGER,
  ADD COLUMN IF NOT EXISTS rerank_blend_weight DOUBLE PRECISION;
