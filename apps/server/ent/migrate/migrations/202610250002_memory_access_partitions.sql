-- Access partitions are server-managed. Empty partition means workspace-wide
-- visibility; it never means all partitions.
CREATE TABLE memory_partitions (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
 agent_id TEXT NOT NULL, audience_actor_ids JSONB NOT NULL DEFAULT '[]',
 revision BIGINT NOT NULL DEFAULT 1, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,workspace_id,id),
 FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id),
 FOREIGN KEY(user_id,agent_id) REFERENCES agents(user_id,id),
 CHECK (jsonb_typeof(audience_actor_ids)='array' AND jsonb_array_length(audience_actor_ids)>0)
);
ALTER TABLE memory_nodes ADD COLUMN partition_id TEXT NOT NULL DEFAULT '',
 ADD COLUMN access_status TEXT NOT NULL DEFAULT 'active',
 ADD COLUMN subject_actor_ids JSONB NOT NULL DEFAULT '[]',
 ADD COLUMN source_refs JSONB NOT NULL DEFAULT '[]',
 ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
CREATE INDEX memory_nodes_access ON memory_nodes(user_id,workspace_id,partition_id,access_status,generation_id);
ALTER TABLE memory_nodes DROP CONSTRAINT memory_nodes_user_id_node_type_entity_id_key;
CREATE UNIQUE INDEX memory_nodes_scoped_entity ON memory_nodes(user_id,workspace_id,partition_id,node_type,entity_id);
ALTER TABLE conversations ADD COLUMN partition_id TEXT NOT NULL DEFAULT '',
 ADD COLUMN context_mode TEXT NOT NULL DEFAULT 'client_history';
ALTER TABLE key_memories ADD COLUMN workspace_id TEXT,
 ADD COLUMN partition_id TEXT NOT NULL DEFAULT '';
ALTER TABLE key_memories ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE key_memories DROP CONSTRAINT IF EXISTS key_memories_user_id_key_key;
CREATE UNIQUE INDEX key_memories_scoped_key ON key_memories(user_id,workspace_id,partition_id,key);

-- Persisted child scope must exactly match its authoritative node.
CREATE FUNCTION memstation_key_scope() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent memory_nodes%ROWTYPE;
BEGIN
 SELECT * INTO STRICT parent FROM memory_nodes WHERE id=NEW.node_id AND user_id=NEW.user_id;
 IF NEW.workspace_id IS DISTINCT FROM parent.workspace_id OR NEW.partition_id IS DISTINCT FROM parent.partition_id THEN
  RAISE EXCEPTION 'key memory scope mismatch';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER key_memory_scope BEFORE INSERT OR UPDATE ON key_memories
 FOR EACH ROW EXECUTE FUNCTION memstation_key_scope();

CREATE FUNCTION memstation_node_partition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.partition_id<>OLD.partition_id) THEN
  RAISE EXCEPTION 'memory partition is immutable';
 END IF;
 IF NEW.partition_id<>'' AND NOT EXISTS(SELECT 1 FROM memory_partitions p
  WHERE p.user_id=NEW.user_id AND p.workspace_id=NEW.workspace_id AND p.id=NEW.partition_id) THEN
  RAISE EXCEPTION 'memory partition not found';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER memory_node_partition BEFORE INSERT OR UPDATE ON memory_nodes
 FOR EACH ROW EXECUTE FUNCTION memstation_node_partition();