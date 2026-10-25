-- Partition is inherited from the authoritative parent, never from JSON metadata.
ALTER TABLE blob_objects ADD COLUMN partition_id TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN partition_id TEXT NOT NULL DEFAULT '';
ALTER TABLE message_parts ADD COLUMN partition_id TEXT NOT NULL DEFAULT '';
ALTER TABLE message_part_artifacts ADD COLUMN partition_id TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_source_snapshots ADD COLUMN partition_id TEXT NOT NULL DEFAULT '', ADD COLUMN content_status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE memory_source_messages ADD COLUMN partition_id TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_generation_sources ADD COLUMN partition_id TEXT NOT NULL DEFAULT '';

CREATE FUNCTION memstation_conversation_partition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent conversations%ROWTYPE;
BEGIN
 SELECT * INTO parent FROM conversations WHERE user_id=NEW.user_id AND workspace_id=NEW.workspace_id AND id=NEW.conversation_id;
 IF FOUND THEN
  IF parent.finalize_status='invalidated' AND TG_OP='INSERT' THEN RAISE EXCEPTION 'source is invalidated'; END IF;
  IF NEW.partition_id<>'' AND NEW.partition_id<>parent.partition_id THEN RAISE EXCEPTION 'source partition mismatch'; END IF;
  NEW.partition_id:=parent.partition_id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER message_partition BEFORE INSERT OR UPDATE ON messages FOR EACH ROW EXECUTE FUNCTION memstation_conversation_partition();
CREATE TRIGGER message_part_partition BEFORE INSERT OR UPDATE ON message_parts FOR EACH ROW EXECUTE FUNCTION memstation_conversation_partition();
CREATE TRIGGER artifact_partition BEFORE INSERT OR UPDATE ON message_part_artifacts FOR EACH ROW EXECUTE FUNCTION memstation_conversation_partition();
CREATE TRIGGER snapshot_partition BEFORE INSERT OR UPDATE ON memory_source_snapshots FOR EACH ROW EXECUTE FUNCTION memstation_conversation_partition();

CREATE FUNCTION memstation_snapshot_partition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent memory_source_snapshots%ROWTYPE;
BEGIN
 SELECT * INTO STRICT parent FROM memory_source_snapshots WHERE user_id=NEW.user_id AND workspace_id=NEW.workspace_id AND snapshot_id=NEW.snapshot_id;
 IF parent.content_status<>'active' AND TG_OP='INSERT' THEN RAISE EXCEPTION 'snapshot is invalidated'; END IF;
 IF NEW.partition_id<>'' AND NEW.partition_id<>parent.partition_id THEN RAISE EXCEPTION 'snapshot partition mismatch'; END IF;
 NEW.partition_id:=parent.partition_id;
 RETURN NEW;
END $$;
CREATE TRIGGER snapshot_message_partition BEFORE INSERT OR UPDATE ON memory_source_messages FOR EACH ROW EXECUTE FUNCTION memstation_snapshot_partition();
CREATE TRIGGER generation_source_partition BEFORE INSERT OR UPDATE ON memory_generation_sources FOR EACH ROW EXECUTE FUNCTION memstation_snapshot_partition();
CREATE INDEX source_snapshot_access ON memory_source_snapshots(user_id,workspace_id,partition_id,content_status);
CREATE INDEX blob_partition_access ON blob_objects(user_id,workspace_id,partition_id);
