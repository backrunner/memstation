-- Maintenance migration. Other workspaces remain disabled until source,
-- generation, visibility and lifecycle isolation are complete.
ALTER TABLE memory_nodes ADD COLUMN workspace_id TEXT;
ALTER TABLE memory_embeddings ADD COLUMN workspace_id TEXT;

DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM memory_embeddings WHERE workspace_id IS NULL) THEN
  RAISE EXCEPTION 'memory scope backfill requires review of orphan or cross-owner embeddings';
 END IF;
END $$;

ALTER TABLE memory_nodes ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE memory_embeddings ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE memory_nodes ADD CONSTRAINT memory_nodes_owner_workspace
 FOREIGN KEY (user_id,workspace_id) REFERENCES workspaces(user_id,id);
ALTER TABLE memory_nodes ADD CONSTRAINT memory_nodes_scoped_identity UNIQUE (user_id,workspace_id,id);
ALTER TABLE memory_embeddings ADD CONSTRAINT memory_embeddings_scoped_node
 FOREIGN KEY (user_id,workspace_id,node_id) REFERENCES memory_nodes(user_id,workspace_id,id) ON DELETE CASCADE;
CREATE INDEX memory_nodes_workspace_activity ON memory_nodes(user_id,workspace_id,node_type,activity_at DESC);
CREATE INDEX memory_embeddings_workspace_generation ON memory_embeddings(user_id,workspace_id,generation_id,embedding_version,embedding_model);

CREATE FUNCTION memstation_immutable_memory_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.user_id,NEW.workspace_id) IS DISTINCT FROM (OLD.user_id,OLD.workspace_id) THEN
  RAISE EXCEPTION 'memory ownership is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER memory_nodes_immutable_scope BEFORE UPDATE ON memory_nodes FOR EACH ROW EXECUTE FUNCTION memstation_immutable_memory_scope();
CREATE TRIGGER memory_embeddings_immutable_scope BEFORE UPDATE ON memory_embeddings FOR EACH ROW EXECUTE FUNCTION memstation_immutable_memory_scope();