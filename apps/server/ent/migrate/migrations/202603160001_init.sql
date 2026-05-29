CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT,
  is_admin BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS api_keys (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL,
  hash TEXT NOT NULL,
  scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
  expires_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_settings (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL UNIQUE,
  chat_model TEXT,
  summary_model TEXT,
  analysis_model TEXT,
  embedding_model TEXT,
  embedding_base_url TEXT,
  embedding_api_key_encrypted TEXT,
  retain_source_materials BOOLEAN NOT NULL DEFAULT TRUE,
  tool_write_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  max_injection_tokens INTEGER NOT NULL DEFAULT 2048,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS provider_credentials (
  provider TEXT PRIMARY KEY,
  encrypted_api_key TEXT,
  base_url TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS system_settings (
  id TEXT PRIMARY KEY,
  default_provider TEXT,
  vector_store_backend TEXT,
  conversation_timeout_seconds INTEGER,
  provider_timeout_seconds INTEGER,
  message_threshold INTEGER,
  token_threshold INTEGER,
  max_context_tokens INTEGER,
  max_injection_tokens INTEGER,
  memory_relation_max_hops INTEGER,
  memory_relation_hop_decay_lambda DOUBLE PRECISION,
  memory_relation_hop_decay_exponent DOUBLE PRECISION,
  rate_limit_rps INTEGER,
  rate_limit_burst INTEGER,
  cache_ttl_seconds INTEGER,
  cors_allowed_origins JSONB,
  session_cookie_cross_site BOOLEAN,
  multimodal_enabled BOOLEAN,
  multimodal_storage_backend TEXT,
  multimodal_storage_local_root TEXT,
  multimodal_storage_bucket TEXT,
  multimodal_extract_image_model TEXT,
  multimodal_extract_audio_model TEXT,
  multimodal_extract_video_model TEXT,
  multimodal_extract_file_model TEXT,
  multimodal_audio_transcription_enabled BOOLEAN,
  multimodal_video_frame_sampling_enabled BOOLEAN,
  multimodal_max_asset_bytes BIGINT,
  multimodal_allowed_mime_types JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  request_format TEXT NOT NULL,
  upstream_provider TEXT,
  mode TEXT NOT NULL DEFAULT 'transparent',
  finalize_status TEXT NOT NULL DEFAULT 'active',
  summary_status TEXT NOT NULL DEFAULT 'pending',
  analysis_status TEXT NOT NULL DEFAULT 'pending',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  role TEXT NOT NULL,
  content_blocks JSONB NOT NULL DEFAULT '[]'::jsonb,
  text_content TEXT,
  tool_calls JSONB NOT NULL DEFAULT '[]'::jsonb,
  tool_result JSONB NOT NULL DEFAULT '{}'::jsonb,
  provider_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  sequence_num INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS token_usages (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  provider TEXT,
  model TEXT,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  cost DOUBLE PRECISION NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS blob_objects (
  blob_id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  source_kind TEXT NOT NULL,
  storage_backend TEXT NOT NULL,
  storage_key TEXT NOT NULL,
  mime_type TEXT,
  sha256 TEXT,
  size_bytes BIGINT NOT NULL DEFAULT 0,
  original_filename TEXT,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_blob_objects_user_created_at
  ON blob_objects (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS message_parts (
  part_id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  sequence_num INTEGER NOT NULL DEFAULT 0,
  part_index INTEGER NOT NULL DEFAULT 0,
  role TEXT NOT NULL,
  part_type TEXT NOT NULL,
  mime_type TEXT,
  blob_id TEXT,
  source_url TEXT,
  provider_file_id TEXT,
  text_content TEXT,
  raw_payload TEXT,
  page_index INTEGER NOT NULL DEFAULT 0,
  start_ms BIGINT NOT NULL DEFAULT 0,
  end_ms BIGINT NOT NULL DEFAULT 0,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_message_parts_timeline
  ON message_parts (user_id, conversation_id, sequence_num, part_index, start_ms);

CREATE TABLE IF NOT EXISTS message_part_artifacts (
  artifact_id TEXT PRIMARY KEY,
  part_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  artifact_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  extractor_kind TEXT,
  model_name TEXT,
  text_content TEXT,
  page_index INTEGER NOT NULL DEFAULT 0,
  start_ms BIGINT NOT NULL DEFAULT 0,
  end_ms BIGINT NOT NULL DEFAULT 0,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_message_part_artifacts_conversation
  ON message_part_artifacts (user_id, conversation_id, part_id, start_ms);

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  actor_type TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id TEXT NOT NULL,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS outbox_events (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  aggregate_type TEXT NOT NULL,
  aggregate_id TEXT NOT NULL,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  status TEXT NOT NULL DEFAULT 'pending',
  available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  processed_at TIMESTAMPTZ,
  retry_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memory_nodes (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  node_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  importance DOUBLE PRECISION NOT NULL DEFAULT 1,
  decay_factor DOUBLE PRECISION NOT NULL DEFAULT 1,
  activity_at TIMESTAMPTZ,
  time_tier TEXT NOT NULL DEFAULT '',
  embedding_status TEXT NOT NULL DEFAULT 'pending',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, node_type, entity_id)
);

CREATE INDEX IF NOT EXISTS idx_memory_nodes_activity_at ON memory_nodes (user_id, activity_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_nodes_time_tier ON memory_nodes (user_id, time_tier, activity_at DESC);

CREATE TABLE IF NOT EXISTS memory_relations (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  src_node_id TEXT NOT NULL,
  dst_node_id TEXT NOT NULL,
  relation_type TEXT NOT NULL,
  weight DOUBLE PRECISION NOT NULL DEFAULT 1,
  source TEXT NOT NULL DEFAULT 'analysis',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, src_node_id, dst_node_id, relation_type)
);

CREATE TABLE IF NOT EXISTS memory_summaries (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  node_id TEXT NOT NULL UNIQUE,
  summary TEXT NOT NULL,
  topic_label TEXT,
  summary_version INTEGER NOT NULL DEFAULT 1,
  chunk_index INTEGER NOT NULL DEFAULT 0,
  chunk_total INTEGER NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memory_keywords (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  summary_id TEXT NOT NULL,
  keyword TEXT NOT NULL,
  weight DOUBLE PRECISION NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS impressions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  node_id TEXT NOT NULL UNIQUE,
  subject TEXT,
  subject_type TEXT,
  evaluation TEXT,
  sentiment TEXT,
  familiarity TEXT,
  description TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS facts (
  id TEXT PRIMARY KEY,
  generation_id TEXT NOT NULL DEFAULT '',
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  node_id TEXT NOT NULL UNIQUE,
  category TEXT,
  subject TEXT,
  fact_key TEXT,
  lineage_group_id TEXT,
  revision_seq INTEGER NOT NULL DEFAULT 1,
  content TEXT NOT NULL,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.8,
  is_superseded BOOLEAN NOT NULL DEFAULT FALSE,
  superseded_by_id TEXT,
  valid_from TIMESTAMPTZ,
  valid_to TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_facts_lineage_group
  ON facts (user_id, generation_id, lineage_group_id, revision_seq);

CREATE INDEX IF NOT EXISTS idx_facts_current_head
  ON facts (user_id, generation_id, lineage_group_id, updated_at DESC)
  WHERE is_superseded = FALSE AND superseded_by_id IS NULL;

CREATE TABLE IF NOT EXISTS key_memories (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  node_id TEXT NOT NULL UNIQUE,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  category TEXT,
  source TEXT,
  review_status TEXT NOT NULL DEFAULT 'pending',
  last_confirmed_at TIMESTAMPTZ,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, key)
);

CREATE TABLE IF NOT EXISTS memory_embeddings (
  embedding_id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  node_id TEXT NOT NULL,
  node_type TEXT NOT NULL,
  embedding_version INTEGER NOT NULL DEFAULT 1,
  embedding_model TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  vector VECTOR,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, node_id, embedding_version)
);

CREATE TABLE IF NOT EXISTS memory_evidences (
  id TEXT PRIMARY KEY,
  generation_id TEXT NOT NULL DEFAULT '',
  user_id TEXT NOT NULL,
  node_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  sequence_num INTEGER NOT NULL DEFAULT 0,
  part_id TEXT,
  artifact_id TEXT,
  page_index INTEGER NOT NULL DEFAULT 0,
  start_ms BIGINT NOT NULL DEFAULT 0,
  end_ms BIGINT NOT NULL DEFAULT 0,
  evidence_kind TEXT NOT NULL,
  excerpt_text TEXT,
  weight DOUBLE PRECISION NOT NULL DEFAULT 1,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_evidences_node
  ON memory_evidences (user_id, node_id, sequence_num, start_ms);
