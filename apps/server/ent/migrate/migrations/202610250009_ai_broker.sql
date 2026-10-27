CREATE TABLE memory_ai_calls (
 id text PRIMARY KEY,user_id text NOT NULL,workspace_id text NOT NULL,partition_id text NOT NULL DEFAULT '',partition_ids jsonb NOT NULL DEFAULT '[""]',
 role text NOT NULL,model_alias text NOT NULL,model_version text NOT NULL DEFAULT '',trace_id text NOT NULL DEFAULT '',
 job_id text NOT NULL DEFAULT '',attempt integer NOT NULL DEFAULT 0,provider_request_id text NOT NULL DEFAULT '',broker_usage_id text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'started',input_tokens integer NOT NULL DEFAULT 0,output_tokens integer NOT NULL DEFAULT 0,
 reported_cost text NOT NULL DEFAULT '',currency text NOT NULL DEFAULT '',usage_authority text NOT NULL DEFAULT 'broker',failure_code text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id)
);
CREATE INDEX memory_ai_calls_scope ON memory_ai_calls(user_id,workspace_id,partition_id,created_at,id);
CREATE UNIQUE INDEX memory_ai_calls_broker_usage ON memory_ai_calls(broker_usage_id) WHERE broker_usage_id<>'';
CREATE TABLE embedding_index_profiles (
 id text PRIMARY KEY,user_id text NOT NULL,workspace_id text NOT NULL,generation_id text NOT NULL,embedding_version integer NOT NULL,
 model_version text NOT NULL,dimensions integer NOT NULL CHECK(dimensions>0),fingerprint text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(user_id,workspace_id,generation_id,embedding_version),FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id)
);