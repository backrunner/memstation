CREATE TABLE integrations (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, name TEXT NOT NULL, kind TEXT NOT NULL,
 identity_namespace_id TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(user_id,id)
);
CREATE TABLE workspace_sources (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, integration_id TEXT NOT NULL,
 external_space_id TEXT NOT NULL, revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,integration_id,external_space_id), UNIQUE(user_id,workspace_id,integration_id,external_space_id),
 FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id),
 FOREIGN KEY(user_id,integration_id) REFERENCES integrations(user_id,id)
);
CREATE TABLE integration_agent_grants (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, integration_id TEXT NOT NULL,
 agent_id TEXT NOT NULL, allowed_event_types JSONB NOT NULL DEFAULT '[]', revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,workspace_id,integration_id,agent_id),
 FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id),
 FOREIGN KEY(user_id,integration_id) REFERENCES integrations(user_id,id),
 FOREIGN KEY(user_id,agent_id) REFERENCES agents(user_id,id)
);
CREATE TABLE actor_identities (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, identity_namespace_id TEXT NOT NULL,
 external_actor_id TEXT NOT NULL, actor_id TEXT NOT NULL, verification_status TEXT NOT NULL DEFAULT 'integration',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,identity_namespace_id,external_actor_id), FOREIGN KEY(user_id,actor_id) REFERENCES actors(user_id,id)
);
CREATE TABLE workspace_members (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, actor_id TEXT NOT NULL,
 display_name TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,workspace_id,actor_id), FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id),
 FOREIGN KEY(user_id,actor_id) REFERENCES actors(user_id,id)
);
CREATE TABLE memory_streams (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, integration_id TEXT NOT NULL,
 external_space_id TEXT NOT NULL, thread_id TEXT NOT NULL DEFAULT '', partition_id TEXT NOT NULL DEFAULT '',
 accepted_sequence BIGINT NOT NULL DEFAULT 0, indexed_sequence BIGINT NOT NULL DEFAULT 0,
 current_conversation_id TEXT NOT NULL DEFAULT '', generation_id TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,workspace_id,id), UNIQUE(user_id,workspace_id,integration_id,external_space_id,thread_id,partition_id),
 FOREIGN KEY(user_id,workspace_id,integration_id,external_space_id) REFERENCES workspace_sources(user_id,workspace_id,integration_id,external_space_id)
);
CREATE TABLE source_messages (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, integration_id TEXT NOT NULL,
 external_message_id TEXT NOT NULL, partition_id TEXT NOT NULL DEFAULT '', stream_id TEXT NOT NULL,
 revision BIGINT NOT NULL, speaker_actor_id TEXT NOT NULL DEFAULT '', parts JSONB NOT NULL DEFAULT '[]',
 reply_to JSONB, occurred_at TIMESTAMPTZ NOT NULL, tombstoned BOOLEAN NOT NULL DEFAULT false,
 payload_hash TEXT NOT NULL, content_status TEXT NOT NULL DEFAULT 'active',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,workspace_id,id), UNIQUE(user_id,workspace_id,integration_id,external_message_id),
 FOREIGN KEY(user_id,workspace_id,stream_id) REFERENCES memory_streams(user_id,workspace_id,id)
);
CREATE TABLE source_events (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, integration_id TEXT NOT NULL,
 external_event_id TEXT NOT NULL, source_message_id TEXT NOT NULL, source_revision BIGINT NOT NULL,
 stream_id TEXT NOT NULL, sequence_num BIGINT NOT NULL, event_type TEXT NOT NULL,
 payload_hash TEXT NOT NULL, payload JSONB NOT NULL, conversation_id TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,workspace_id,integration_id,external_event_id), UNIQUE(stream_id,sequence_num),
 FOREIGN KEY(user_id,workspace_id,source_message_id) REFERENCES source_messages(user_id,workspace_id,id)
);
CREATE TABLE ingestion_receipts (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, partition_id TEXT NOT NULL DEFAULT '',
 stream_id TEXT NOT NULL, idempotency_key TEXT NOT NULL, payload_hash TEXT NOT NULL,
 first_sequence BIGINT NOT NULL, last_sequence BIGINT NOT NULL, events JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,workspace_id,stream_id,idempotency_key),
 FOREIGN KEY(user_id,workspace_id,stream_id) REFERENCES memory_streams(user_id,workspace_id,id)
);
CREATE TABLE ingestion_window_sources (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, conversation_id TEXT NOT NULL,
 source_message_id TEXT NOT NULL, source_revision BIGINT NOT NULL, sequence_num INTEGER NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(conversation_id,source_message_id), UNIQUE(conversation_id,sequence_num),
 FOREIGN KEY(user_id,workspace_id,conversation_id) REFERENCES conversations(user_id,workspace_id,id),
 FOREIGN KEY(user_id,workspace_id,source_message_id) REFERENCES source_messages(user_id,workspace_id,id)
);
CREATE TABLE memory_source_dependencies (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, node_id TEXT NOT NULL,
 source_message_id TEXT NOT NULL, source_revision BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(node_id,source_message_id,source_revision),
 FOREIGN KEY(user_id,workspace_id,node_id) REFERENCES memory_nodes(user_id,workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY(user_id,workspace_id,source_message_id) REFERENCES source_messages(user_id,workspace_id,id)
);
CREATE TABLE memory_jobs (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, partition_id TEXT NOT NULL DEFAULT '',
 conversation_id TEXT NOT NULL DEFAULT '', task_type TEXT NOT NULL, status TEXT NOT NULL,
 attempt INTEGER NOT NULL DEFAULT 0, failure_code TEXT NOT NULL DEFAULT '', next_retry_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX memory_jobs_workspace_status ON memory_jobs(user_id,workspace_id,partition_id,status,updated_at);
CREATE INDEX source_dependencies_message ON memory_source_dependencies(user_id,workspace_id,source_message_id);
CREATE INDEX source_events_window ON source_events(conversation_id);

ALTER TABLE memory_nodes ADD COLUMN source_conversation_id TEXT NOT NULL DEFAULT '';
CREATE INDEX memory_nodes_source_window ON memory_nodes(user_id,workspace_id,source_conversation_id);
