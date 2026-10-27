CREATE TABLE memory_candidates (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL,workspace_id TEXT NOT NULL,partition_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL,kind TEXT NOT NULL,content TEXT NOT NULL,subject_actor_ids JSONB NOT NULL DEFAULT '[]',
 source_refs JSONB NOT NULL DEFAULT '[]',origin TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',revision BIGINT NOT NULL DEFAULT 1,
 target_node_id TEXT NOT NULL DEFAULT '',target_revision BIGINT NOT NULL DEFAULT 0,result_node_id TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id)
);
CREATE INDEX memory_candidates_scope ON memory_candidates(user_id,workspace_id,partition_id,status,created_at);
CREATE TABLE memory_publications (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL,source_workspace_id TEXT NOT NULL,source_node_id TEXT NOT NULL,source_revision BIGINT NOT NULL,
 target_workspace_id TEXT NOT NULL,target_partition_id TEXT NOT NULL DEFAULT '',target_node_id TEXT NOT NULL,
 actor_mapping JSONB NOT NULL DEFAULT '{}',status TEXT NOT NULL DEFAULT 'active',revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(target_node_id),FOREIGN KEY(user_id,source_workspace_id,source_node_id) REFERENCES memory_nodes(user_id,workspace_id,id),
 FOREIGN KEY(user_id,target_workspace_id,target_node_id) REFERENCES memory_nodes(user_id,workspace_id,id)
);
CREATE TABLE memory_operations (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL,workspace_id TEXT NOT NULL,partition_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',revision BIGINT NOT NULL DEFAULT 1,
 selector JSONB NOT NULL,read_blocked BOOLEAN NOT NULL DEFAULT false,online_cleaned BOOLEAN NOT NULL DEFAULT false,
 journal_persisted BOOLEAN NOT NULL DEFAULT false,backup_status TEXT NOT NULL DEFAULT 'retained_until_expiry',
 failure_code TEXT NOT NULL DEFAULT '',result JSONB NOT NULL DEFAULT '{}',expires_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id)
);
CREATE INDEX memory_operations_scope ON memory_operations(user_id,workspace_id,partition_id,status,created_at);
CREATE TABLE memory_tombstones (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL,workspace_id TEXT NOT NULL,partition_id TEXT NOT NULL DEFAULT '',
 operation_id TEXT NOT NULL,target_type TEXT NOT NULL,target_id TEXT NOT NULL,cutoff TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,workspace_id,partition_id,target_type,target_id),FOREIGN KEY(operation_id) REFERENCES memory_operations(id)
);
CREATE TABLE deletion_journal_state (
 id TEXT PRIMARY KEY,deployment_id TEXT NOT NULL,sequence_num BIGINT NOT NULL DEFAULT 0,digest TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE deletion_journal_entries (
 id TEXT PRIMARY KEY,sequence_num BIGINT NOT NULL UNIQUE,operation_id TEXT NOT NULL,payload JSONB NOT NULL,digest TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);