ALTER TABLE impressions ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'::jsonb;
