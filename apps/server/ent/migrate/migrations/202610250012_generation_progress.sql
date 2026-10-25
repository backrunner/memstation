ALTER TABLE memory_jobs ADD COLUMN generation_id text NOT NULL DEFAULT '';
CREATE INDEX memory_jobs_generation ON memory_jobs(user_id,workspace_id,generation_id,conversation_id);
