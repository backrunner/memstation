-- Maintenance migration: stop writers/workers before applying. This is the
-- conversation spine only; other workspaces remain disabled in the data plane.
ALTER TABLE conversations ADD COLUMN workspace_id TEXT;
ALTER TABLE conversations ADD COLUMN agent_id TEXT;
ALTER TABLE messages ADD COLUMN workspace_id TEXT;
ALTER TABLE token_usages ADD COLUMN workspace_id TEXT;

ALTER TABLE conversations ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE conversations ALTER COLUMN agent_id SET NOT NULL;
ALTER TABLE messages ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE token_usages ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE conversations ADD CONSTRAINT conversations_owner_workspace FOREIGN KEY (user_id,workspace_id) REFERENCES workspaces(user_id,id);
ALTER TABLE conversations ADD CONSTRAINT conversations_owner_agent FOREIGN KEY (user_id,agent_id) REFERENCES agents(user_id,id);
ALTER TABLE conversations ADD CONSTRAINT conversations_scoped_identity UNIQUE (user_id,workspace_id,id);
ALTER TABLE messages ADD CONSTRAINT messages_scoped_conversation FOREIGN KEY (user_id,workspace_id,conversation_id) REFERENCES conversations(user_id,workspace_id,id);
ALTER TABLE token_usages ADD CONSTRAINT token_usages_scoped_conversation FOREIGN KEY (user_id,workspace_id,conversation_id) REFERENCES conversations(user_id,workspace_id,id);
CREATE INDEX conversations_workspace_activity ON conversations(user_id,workspace_id,updated_at DESC);
CREATE INDEX messages_workspace_sequence ON messages(user_id,workspace_id,conversation_id,sequence_num);
CREATE INDEX token_usages_workspace_conversation ON token_usages(user_id,workspace_id,conversation_id);

CREATE FUNCTION memstation_immutable_conversation_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.user_id,NEW.workspace_id,NEW.agent_id) IS DISTINCT FROM (OLD.user_id,OLD.workspace_id,OLD.agent_id) THEN
  RAISE EXCEPTION 'conversation ownership is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER conversations_immutable_scope BEFORE UPDATE ON conversations FOR EACH ROW EXECUTE FUNCTION memstation_immutable_conversation_scope();
CREATE FUNCTION memstation_immutable_conversation_child_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.user_id,NEW.workspace_id,NEW.conversation_id) IS DISTINCT FROM (OLD.user_id,OLD.workspace_id,OLD.conversation_id) THEN
  RAISE EXCEPTION 'conversation child ownership is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER messages_immutable_scope BEFORE UPDATE ON messages FOR EACH ROW EXECUTE FUNCTION memstation_immutable_conversation_child_scope();
CREATE TRIGGER token_usages_immutable_scope BEFORE UPDATE ON token_usages FOR EACH ROW EXECUTE FUNCTION memstation_immutable_conversation_child_scope();