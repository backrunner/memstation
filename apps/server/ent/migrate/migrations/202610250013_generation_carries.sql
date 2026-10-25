CREATE TABLE memory_generation_carries (
 id text PRIMARY KEY, user_id text NOT NULL, workspace_id text NOT NULL, partition_id text NOT NULL DEFAULT '',
 generation_id text NOT NULL REFERENCES memory_generations(generation_id), node_id text NOT NULL REFERENCES memory_nodes(id) ON DELETE CASCADE,
 source_generation_id text NOT NULL, source_revision bigint NOT NULL, embedding_revision bigint NOT NULL,
 status text NOT NULL DEFAULT 'queued', last_error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(generation_id,node_id)
);
CREATE INDEX memory_generation_carries_scope ON memory_generation_carries(user_id,workspace_id,generation_id);
