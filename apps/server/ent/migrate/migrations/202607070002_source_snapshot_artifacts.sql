ALTER TABLE memory_source_snapshots
  ADD COLUMN IF NOT EXISTS message_parts JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE memory_source_snapshots
  ADD COLUMN IF NOT EXISTS part_artifacts JSONB NOT NULL DEFAULT '[]'::jsonb;
