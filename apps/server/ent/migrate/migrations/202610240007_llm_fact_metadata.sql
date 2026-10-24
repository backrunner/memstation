ALTER TABLE facts ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'::jsonb;
