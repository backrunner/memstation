-- Control-plane expansion only. Non-personal workspaces remain unavailable until
-- every memory, source, vector and worker path carries an explicit workspace.
CREATE TABLE workspaces (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL, name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'personal' CHECK (kind IN ('personal','project','group','custom')),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
  policy_version BIGINT NOT NULL DEFAULT 1 CHECK (policy_version > 0),
  capabilities JSONB NOT NULL CHECK (jsonb_typeof(capabilities) = 'array'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, id)
);
CREATE UNIQUE INDEX workspaces_personal_owner ON workspaces(user_id) WHERE kind='personal';
CREATE TABLE actors (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('human','agent')),
  display_name TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', revision BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, id)
);
CREATE TABLE agents (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL, actor_id TEXT NOT NULL, name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  default_workspace_id TEXT NOT NULL, revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, id), UNIQUE (user_id, actor_id),
  FOREIGN KEY (user_id, actor_id) REFERENCES actors(user_id, id),
  FOREIGN KEY (user_id, default_workspace_id) REFERENCES workspaces(user_id, id)
);
CREATE TABLE workspace_grants (
  id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL, agent_id TEXT NOT NULL,
  capabilities JSONB NOT NULL CHECK (jsonb_typeof(capabilities) = 'array'), revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, workspace_id, agent_id),
  FOREIGN KEY (user_id, workspace_id) REFERENCES workspaces(user_id, id),
  FOREIGN KEY (user_id, agent_id) REFERENCES agents(user_id, id)
);
ALTER TABLE api_keys ADD COLUMN agent_id TEXT;
ALTER TABLE api_keys ADD COLUMN default_workspace_id TEXT;
ALTER TABLE api_keys ADD COLUMN capability_ceiling JSONB;

ALTER TABLE api_keys ALTER COLUMN agent_id SET NOT NULL;
ALTER TABLE api_keys ALTER COLUMN default_workspace_id SET NOT NULL;
ALTER TABLE api_keys ALTER COLUMN capability_ceiling SET NOT NULL;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_owner_agent FOREIGN KEY (user_id,agent_id) REFERENCES agents(user_id,id);
ALTER TABLE api_keys ADD CONSTRAINT api_keys_owner_workspace FOREIGN KEY (user_id,default_workspace_id) REFERENCES workspaces(user_id,id);
CREATE INDEX api_keys_identity_lookup ON api_keys(prefix,hash) WHERE revoked_at IS NULL AND deleted_at IS NULL;
CREATE INDEX api_keys_agent ON api_keys(user_id,agent_id);

ALTER TABLE conversations ADD COLUMN write_revision BIGINT NOT NULL DEFAULT 0;