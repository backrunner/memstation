ALTER TABLE source_events ADD COLUMN operation_id text NOT NULL DEFAULT '';
CREATE INDEX source_events_operation_idx ON source_events(operation_id) WHERE operation_id<>'';
ALTER TABLE memory_jobs ADD COLUMN operation_id text NOT NULL DEFAULT '';
CREATE INDEX memory_jobs_operation_idx ON memory_jobs(operation_id) WHERE operation_id<>'';
ALTER TABLE ingestion_receipts ADD COLUMN processing_started_at timestamptz;