CREATE TABLE IF NOT EXISTS memory_items (
  id TEXT PRIMARY KEY,
  generation_id TEXT NOT NULL DEFAULT '',
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL DEFAULT '',
  node_id TEXT NOT NULL UNIQUE,
  item_type TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'current',
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.75,
  valid_from TIMESTAMPTZ,
  valid_to TIMESTAMPTZ,
  review_status TEXT NOT NULL DEFAULT 'auto',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT memory_items_item_type_check CHECK (
    item_type IN ('event', 'task', 'procedure', 'artifact', 'entity', 'agent_action')
  )
);

CREATE INDEX IF NOT EXISTS idx_memory_items_user_generation_type_updated
  ON memory_items (user_id, generation_id, item_type, updated_at DESC)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_items_user_conversation
  ON memory_items (user_id, generation_id, conversation_id, updated_at DESC)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_items_user_activity
  ON memory_items (user_id, updated_at DESC)
  WHERE deleted_at IS NULL;
