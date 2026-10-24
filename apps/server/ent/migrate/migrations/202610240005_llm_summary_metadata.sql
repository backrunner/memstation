ALTER TABLE memory_summaries ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'::jsonb;
