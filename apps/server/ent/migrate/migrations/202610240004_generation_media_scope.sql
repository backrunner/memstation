-- Maintenance migration: source and lifecycle scope. Additional workspaces stay disabled.
ALTER TABLE memory_generations ADD COLUMN workspace_id TEXT;
ALTER TABLE memory_source_snapshots ADD COLUMN workspace_id TEXT;
ALTER TABLE memory_source_messages ADD COLUMN workspace_id TEXT;
ALTER TABLE memory_generation_sources ADD COLUMN workspace_id TEXT;
ALTER TABLE blob_objects ADD COLUMN workspace_id TEXT;
ALTER TABLE message_parts ADD COLUMN workspace_id TEXT;
ALTER TABLE message_part_artifacts ADD COLUMN workspace_id TEXT;

INSERT INTO workspaces(id,user_id,name,capabilities)
SELECT DISTINCT 'personal-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'),user_id,'Personal',
 '["gateway.invoke","memory.read","memory.edit","files.read","files.write"]'::jsonb
FROM (SELECT user_id FROM memory_generations UNION SELECT user_id FROM memory_source_snapshots UNION SELECT user_id FROM memory_source_messages UNION SELECT user_id FROM memory_generation_sources UNION SELECT user_id FROM blob_objects UNION SELECT user_id FROM message_parts UNION SELECT user_id FROM message_part_artifacts) owners ON CONFLICT DO NOTHING;
UPDATE memory_generations SET workspace_id='personal-' || encode(sha256(convert_to(user_id,'UTF8')),'hex');
UPDATE memory_source_snapshots SET workspace_id='personal-' || encode(sha256(convert_to(user_id,'UTF8')),'hex');
UPDATE blob_objects SET workspace_id='personal-' || encode(sha256(convert_to(user_id,'UTF8')),'hex');
-- Retained snapshots may outlive live conversation rows. An existing parent
-- must agree with the snapshot owner; otherwise require explicit repair.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM memory_source_snapshots s JOIN conversations c ON c.id=s.conversation_id WHERE c.user_id<>s.user_id OR c.workspace_id<>s.workspace_id) THEN
  RAISE EXCEPTION 'source scope backfill requires review of conversation ownership';
 END IF;
END $$;
UPDATE memory_source_messages m SET workspace_id=s.workspace_id FROM memory_source_snapshots s WHERE s.snapshot_id=m.snapshot_id AND s.user_id=m.user_id AND s.conversation_id=m.conversation_id AND s.snapshot_version=m.snapshot_version;
UPDATE memory_generation_sources source SET workspace_id=g.workspace_id FROM memory_generations g,memory_source_snapshots s WHERE g.generation_id=source.generation_id AND g.user_id=source.user_id AND s.snapshot_id=source.snapshot_id AND s.user_id=source.user_id AND s.workspace_id=g.workspace_id AND s.conversation_id=source.conversation_id;
UPDATE message_parts p SET workspace_id=c.workspace_id FROM conversations c WHERE c.id=p.conversation_id AND c.user_id=p.user_id;
UPDATE message_part_artifacts a SET workspace_id=p.workspace_id FROM message_parts p WHERE p.part_id=a.part_id AND p.user_id=a.user_id AND p.conversation_id=a.conversation_id;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM memory_source_messages WHERE workspace_id IS NULL)
 OR EXISTS (SELECT 1 FROM memory_generation_sources WHERE workspace_id IS NULL)
 OR EXISTS (SELECT 1 FROM message_parts WHERE workspace_id IS NULL)
 OR EXISTS (SELECT 1 FROM message_part_artifacts WHERE workspace_id IS NULL)
 OR EXISTS (SELECT 1 FROM message_parts p LEFT JOIN blob_objects b ON b.blob_id=p.blob_id AND b.user_id=p.user_id AND b.workspace_id=p.workspace_id WHERE p.blob_id IS NOT NULL AND b.blob_id IS NULL) THEN
  RAISE EXCEPTION 'source scope backfill requires review of orphan or cross-scope references';
 END IF;
END $$;
ALTER TABLE memory_generations ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE memory_generations ADD CONSTRAINT memory_generations_owner_workspace FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id);
CREATE TRIGGER memory_generations_immutable_scope BEFORE UPDATE ON memory_generations FOR EACH ROW EXECUTE FUNCTION memstation_immutable_memory_scope();
ALTER TABLE memory_source_snapshots ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE memory_source_snapshots ADD CONSTRAINT memory_source_snapshots_owner_workspace FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id);
CREATE TRIGGER memory_source_snapshots_immutable_scope BEFORE UPDATE ON memory_source_snapshots FOR EACH ROW EXECUTE FUNCTION memstation_immutable_memory_scope();
ALTER TABLE memory_source_messages ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE memory_source_messages ADD CONSTRAINT memory_source_messages_owner_workspace FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id);
CREATE TRIGGER memory_source_messages_immutable_scope BEFORE UPDATE ON memory_source_messages FOR EACH ROW EXECUTE FUNCTION memstation_immutable_memory_scope();
ALTER TABLE memory_generation_sources ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE memory_generation_sources ADD CONSTRAINT memory_generation_sources_owner_workspace FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id);
CREATE TRIGGER memory_generation_sources_immutable_scope BEFORE UPDATE ON memory_generation_sources FOR EACH ROW EXECUTE FUNCTION memstation_immutable_memory_scope();
ALTER TABLE blob_objects ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE blob_objects ADD CONSTRAINT blob_objects_owner_workspace FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id);
CREATE TRIGGER blob_objects_immutable_scope BEFORE UPDATE ON blob_objects FOR EACH ROW EXECUTE FUNCTION memstation_immutable_memory_scope();
ALTER TABLE message_parts ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE message_parts ADD CONSTRAINT message_parts_owner_workspace FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id);
CREATE TRIGGER message_parts_immutable_scope BEFORE UPDATE ON message_parts FOR EACH ROW EXECUTE FUNCTION memstation_immutable_memory_scope();
ALTER TABLE message_part_artifacts ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE message_part_artifacts ADD CONSTRAINT message_part_artifacts_owner_workspace FOREIGN KEY(user_id,workspace_id) REFERENCES workspaces(user_id,id);
CREATE TRIGGER message_part_artifacts_immutable_scope BEFORE UPDATE ON message_part_artifacts FOR EACH ROW EXECUTE FUNCTION memstation_immutable_memory_scope();
DROP INDEX memory_generations_one_active_per_user_idx;
CREATE UNIQUE INDEX memory_generations_one_active_per_workspace_idx ON memory_generations(user_id,workspace_id) WHERE is_active;
ALTER TABLE memory_generations ADD CONSTRAINT memory_generations_scoped_id UNIQUE(user_id,workspace_id,generation_id);
ALTER TABLE memory_source_snapshots ADD CONSTRAINT memory_source_snapshots_scoped_id UNIQUE(user_id,workspace_id,snapshot_id,conversation_id);
ALTER TABLE memory_source_snapshots ADD CONSTRAINT memory_source_snapshots_scoped_version UNIQUE(user_id,workspace_id,snapshot_id,conversation_id,snapshot_version);
ALTER TABLE memory_source_messages ADD CONSTRAINT source_messages_scoped_snapshot FOREIGN KEY(user_id,workspace_id,snapshot_id,conversation_id,snapshot_version) REFERENCES memory_source_snapshots(user_id,workspace_id,snapshot_id,conversation_id,snapshot_version) ON DELETE CASCADE;
ALTER TABLE memory_generation_sources ADD CONSTRAINT generation_sources_scoped_generation FOREIGN KEY(user_id,workspace_id,generation_id) REFERENCES memory_generations(user_id,workspace_id,generation_id) ON DELETE CASCADE;
ALTER TABLE memory_generation_sources ADD CONSTRAINT generation_sources_scoped_snapshot FOREIGN KEY(user_id,workspace_id,snapshot_id,conversation_id) REFERENCES memory_source_snapshots(user_id,workspace_id,snapshot_id,conversation_id);
ALTER TABLE blob_objects ADD CONSTRAINT blob_objects_scoped_id UNIQUE(user_id,workspace_id,blob_id);
ALTER TABLE message_parts ADD CONSTRAINT message_parts_scoped_id UNIQUE(user_id,workspace_id,conversation_id,part_id);
ALTER TABLE message_parts ADD CONSTRAINT message_parts_scoped_conversation FOREIGN KEY(user_id,workspace_id,conversation_id) REFERENCES conversations(user_id,workspace_id,id);
ALTER TABLE message_parts ADD CONSTRAINT message_parts_scoped_blob FOREIGN KEY(user_id,workspace_id,blob_id) REFERENCES blob_objects(user_id,workspace_id,blob_id);
ALTER TABLE message_part_artifacts ADD CONSTRAINT artifacts_scoped_part FOREIGN KEY(user_id,workspace_id,conversation_id,part_id) REFERENCES message_parts(user_id,workspace_id,conversation_id,part_id) ON DELETE CASCADE;
CREATE INDEX memory_generations_workspace_created ON memory_generations(user_id,workspace_id,created_at DESC);
CREATE INDEX snapshots_workspace_conversation ON memory_source_snapshots(user_id,workspace_id,conversation_id,snapshot_version DESC);
CREATE INDEX blobs_workspace_created ON blob_objects(user_id,workspace_id,created_at DESC);
