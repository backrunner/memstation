CREATE TABLE event_conversation_requests (
 id text PRIMARY KEY,user_id text NOT NULL,workspace_id text NOT NULL,partition_id text NOT NULL DEFAULT '',
 stream_id text NOT NULL,conversation_id text NOT NULL,payload_hash text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(user_id,workspace_id,conversation_id) REFERENCES conversations(user_id,workspace_id,id)
);