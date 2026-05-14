ALTER TABLE user_settings
  ALTER COLUMN retain_source_materials SET DEFAULT TRUE;

ALTER TABLE system_settings
  ADD COLUMN IF NOT EXISTS multimodal_enabled BOOLEAN,
  ADD COLUMN IF NOT EXISTS multimodal_storage_backend TEXT,
  ADD COLUMN IF NOT EXISTS multimodal_storage_local_root TEXT,
  ADD COLUMN IF NOT EXISTS multimodal_storage_bucket TEXT,
  ADD COLUMN IF NOT EXISTS multimodal_extract_image_model TEXT,
  ADD COLUMN IF NOT EXISTS multimodal_extract_audio_model TEXT,
  ADD COLUMN IF NOT EXISTS multimodal_extract_video_model TEXT,
  ADD COLUMN IF NOT EXISTS multimodal_extract_file_model TEXT,
  ADD COLUMN IF NOT EXISTS multimodal_audio_transcription_enabled BOOLEAN,
  ADD COLUMN IF NOT EXISTS multimodal_video_frame_sampling_enabled BOOLEAN,
  ADD COLUMN IF NOT EXISTS multimodal_max_asset_bytes BIGINT,
  ADD COLUMN IF NOT EXISTS multimodal_allowed_mime_types JSONB;

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
