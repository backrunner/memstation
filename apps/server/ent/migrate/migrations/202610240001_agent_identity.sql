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
  legacy BOOLEAN NOT NULL DEFAULT false,
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

-- Keep orphaned legacy owners as owners; never infer that they belong to admin.
WITH owners AS (
 SELECT id AS user_id FROM users UNION SELECT user_id FROM api_keys
 UNION SELECT user_id FROM conversations UNION SELECT user_id FROM memory_nodes
)
INSERT INTO workspaces (id,user_id,name,capabilities)
SELECT 'personal-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'), user_id, 'Personal',
 '["gateway.invoke","memory.read","memory.edit","files.read","files.write"]'::jsonb
FROM owners;
INSERT INTO actors (id,user_id,kind,display_name)
SELECT 'owner-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'), user_id, 'human', 'Owner' FROM workspaces
UNION ALL
SELECT 'legacy-actor-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'), user_id, 'agent', 'Legacy agent' FROM workspaces;
INSERT INTO agents (id,user_id,actor_id,name,default_workspace_id,legacy)
SELECT 'legacy-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'), user_id,
 'legacy-actor-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'), 'Legacy agent', id, true FROM workspaces;
INSERT INTO workspace_grants (id,user_id,workspace_id,agent_id,capabilities)
SELECT a.id || ':personal', a.user_id, w.id, a.id, w.capabilities FROM agents a JOIN workspaces w ON w.user_id=a.user_id;
UPDATE api_keys k SET agent_id=a.id, default_workspace_id=a.default_workspace_id, capability_ceiling=w.capabilities
FROM agents a JOIN workspaces w ON w.id=a.default_workspace_id WHERE a.user_id=k.user_id AND a.legacy;
ALTER TABLE api_keys ALTER COLUMN agent_id SET NOT NULL;
ALTER TABLE api_keys ALTER COLUMN default_workspace_id SET NOT NULL;
ALTER TABLE api_keys ALTER COLUMN capability_ceiling SET NOT NULL;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_owner_agent FOREIGN KEY (user_id,agent_id) REFERENCES agents(user_id,id);
ALTER TABLE api_keys ADD CONSTRAINT api_keys_owner_workspace FOREIGN KEY (user_id,default_workspace_id) REFERENCES workspaces(user_id,id);
CREATE INDEX api_keys_identity_lookup ON api_keys(prefix,hash) WHERE revoked_at IS NULL AND deleted_at IS NULL;
CREATE INDEX api_keys_agent ON api_keys(user_id,agent_id);

ALTER TABLE conversations ADD COLUMN write_revision BIGINT NOT NULL DEFAULT 0;
