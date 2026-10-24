-- Maintenance migration: stop writers/workers before applying. This is the
-- conversation spine only; other workspaces remain disabled in the data plane.
ALTER TABLE conversations ADD COLUMN workspace_id TEXT;
ALTER TABLE conversations ADD COLUMN agent_id TEXT;
ALTER TABLE messages ADD COLUMN workspace_id TEXT;
ALTER TABLE token_usages ADD COLUMN workspace_id TEXT;

-- Preserve orphaned legacy owners. Bootstrap identities without reviving an
-- existing disabled Agent, revoked grant, or archived workspace.
INSERT INTO workspaces (id,user_id,name,capabilities)
SELECT DISTINCT 'personal-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'),user_id,'Personal',
 '["gateway.invoke","memory.read","memory.edit","files.read","files.write"]'::jsonb
FROM conversations ON CONFLICT DO NOTHING;
INSERT INTO actors (id,user_id,kind,display_name)
SELECT 'owner-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'),user_id,'human','Owner'
FROM workspaces WHERE kind='personal' ON CONFLICT DO NOTHING;
INSERT INTO actors (id,user_id,kind,display_name)
SELECT 'legacy-actor-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'),user_id,'agent','Legacy agent'
FROM workspaces WHERE kind='personal' ON CONFLICT DO NOTHING;
INSERT INTO agents (id,user_id,actor_id,name,default_workspace_id,legacy)
SELECT 'legacy-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'),user_id,
 'legacy-actor-' || encode(sha256(convert_to(user_id,'UTF8')),'hex'),'Legacy agent',id,true
FROM workspaces WHERE kind='personal' ON CONFLICT DO NOTHING;
INSERT INTO workspace_grants (id,user_id,workspace_id,agent_id,capabilities)
SELECT a.id || ':personal',a.user_id,w.id,a.id,w.capabilities
FROM agents a JOIN workspaces w ON w.user_id=a.user_id AND w.id=a.default_workspace_id
WHERE a.legacy ON CONFLICT DO NOTHING;

UPDATE conversations c SET workspace_id=w.id,agent_id=a.id
FROM workspaces w,agents a WHERE w.user_id=c.user_id AND w.kind='personal'
 AND a.user_id=c.user_id AND a.id='legacy-' || encode(sha256(convert_to(c.user_id,'UTF8')),'hex');
-- Only foundation-era writes with a real same-owner Agent can preserve a
-- creator from metadata. Older arbitrary metadata is not identity authority.
UPDATE conversations c SET agent_id=a.id FROM agents a
WHERE c.write_revision>0 AND a.user_id=c.user_id AND a.id=c.metadata->>'agent_id';

UPDATE messages m SET workspace_id=c.workspace_id FROM conversations c
WHERE c.id=m.conversation_id AND c.user_id=m.user_id;
UPDATE token_usages u SET workspace_id=c.workspace_id FROM conversations c
WHERE c.id=u.conversation_id AND c.user_id=u.user_id;

-- Ambiguous/orphan children stop the migration intact; no reassignment or loss.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM messages WHERE workspace_id IS NULL)
 OR EXISTS (SELECT 1 FROM token_usages WHERE workspace_id IS NULL) THEN
  RAISE EXCEPTION 'conversation scope backfill requires review of orphan or cross-owner children';
 END IF;
END $$;

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
