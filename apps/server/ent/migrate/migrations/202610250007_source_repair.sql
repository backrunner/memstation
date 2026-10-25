CREATE TABLE source_links (
 id text PRIMARY KEY,user_id text NOT NULL,workspace_id text NOT NULL,source_message_id text NOT NULL,source_revision bigint NOT NULL,
 parent_message_id text NOT NULL,parent_revision bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(source_message_id,source_revision,parent_message_id,parent_revision)
);
CREATE INDEX source_links_parent_idx ON source_links(user_id,workspace_id,parent_message_id);
ALTER TABLE source_events ADD COLUMN processing_conversation_id text NOT NULL DEFAULT '';
UPDATE source_events SET processing_conversation_id=conversation_id;
