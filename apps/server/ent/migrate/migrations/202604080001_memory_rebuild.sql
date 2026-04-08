ALTER TABLE user_settings
  ADD COLUMN IF NOT EXISTS retain_source_materials BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE conversation_segments
  ADD COLUMN IF NOT EXISTS generation_id TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS conversation_segments_user_conversation_index_idx;
CREATE UNIQUE INDEX IF NOT EXISTS conversation_segments_user_conversation_generation_index_idx
  ON conversation_segments (user_id, conversation_id, generation_id, segment_index);

ALTER TABLE memory_nodes
  ADD COLUMN IF NOT EXISTS generation_id TEXT NOT NULL DEFAULT '';

ALTER TABLE memory_relations
  ADD COLUMN IF NOT EXISTS generation_id TEXT NOT NULL DEFAULT '';

ALTER TABLE memory_summaries
  ADD COLUMN IF NOT EXISTS generation_id TEXT NOT NULL DEFAULT '';

ALTER TABLE impressions
  ADD COLUMN IF NOT EXISTS generation_id TEXT NOT NULL DEFAULT '';

ALTER TABLE facts
  ADD COLUMN IF NOT EXISTS generation_id TEXT NOT NULL DEFAULT '';

ALTER TABLE memory_embeddings
  ADD COLUMN IF NOT EXISTS generation_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_memory_nodes_user_generation
  ON memory_nodes (user_id, generation_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_memory_relations_user_generation
  ON memory_relations (user_id, generation_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_memory_embeddings_user_generation_version
  ON memory_embeddings (user_id, generation_id, embedding_version, updated_at DESC);

CREATE TABLE IF NOT EXISTS memory_source_snapshots (
  snapshot_id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  snapshot_version INTEGER NOT NULL,
  source_hash TEXT NOT NULL,
  request_format TEXT NOT NULL,
  upstream_provider TEXT,
  mode TEXT NOT NULL DEFAULT 'transparent',
  finalize_status TEXT NOT NULL DEFAULT 'ended',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  usage JSONB NOT NULL DEFAULT '{}'::jsonb,
  message_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, conversation_id, snapshot_version)
);

CREATE INDEX IF NOT EXISTS idx_memory_source_snapshots_user_conversation
  ON memory_source_snapshots (user_id, conversation_id, snapshot_version DESC);

CREATE TABLE IF NOT EXISTS memory_source_messages (
  id TEXT PRIMARY KEY,
  snapshot_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  snapshot_version INTEGER NOT NULL,
  sequence_num INTEGER NOT NULL DEFAULT 0,
  role TEXT NOT NULL,
  content_blocks JSONB NOT NULL DEFAULT '[]'::jsonb,
  text_content TEXT,
  tool_calls JSONB NOT NULL DEFAULT '[]'::jsonb,
  tool_result JSONB NOT NULL DEFAULT '{}'::jsonb,
  provider_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (snapshot_id, sequence_num)
);

CREATE INDEX IF NOT EXISTS idx_memory_source_messages_snapshot
  ON memory_source_messages (snapshot_id, sequence_num);

CREATE TABLE IF NOT EXISTS memory_generations (
  generation_id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  scope TEXT NOT NULL DEFAULT 'user',
  conversation_id TEXT,
  is_active BOOLEAN NOT NULL DEFAULT FALSE,
  structure_version INTEGER NOT NULL DEFAULT 1,
  embedding_version INTEGER NOT NULL DEFAULT 1,
  embedding_model TEXT NOT NULL DEFAULT '',
  activate_on_success BOOLEAN NOT NULL DEFAULT TRUE,
  source_count INTEGER NOT NULL DEFAULT 0,
  processed_count INTEGER NOT NULL DEFAULT 0,
  embedding_count INTEGER NOT NULL DEFAULT 0,
  failure_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  activated_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS memory_generations_one_active_per_user_idx
  ON memory_generations (user_id)
  WHERE is_active = TRUE;

CREATE INDEX IF NOT EXISTS idx_memory_generations_user_created
  ON memory_generations (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS memory_generation_sources (
  id TEXT PRIMARY KEY,
  generation_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  snapshot_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  source_hash TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  processed_at TIMESTAMPTZ,
  UNIQUE (generation_id, snapshot_id)
);

CREATE INDEX IF NOT EXISTS idx_memory_generation_sources_generation
  ON memory_generation_sources (generation_id, created_at ASC);
