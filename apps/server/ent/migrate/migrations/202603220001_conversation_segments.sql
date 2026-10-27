CREATE UNIQUE INDEX IF NOT EXISTS messages_user_conversation_sequence_idx
  ON messages (user_id, conversation_id, sequence_num);

CREATE TABLE IF NOT EXISTS conversation_segments (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  segment_index INTEGER NOT NULL,
  start_sequence INTEGER NOT NULL,
  end_sequence INTEGER NOT NULL,
  message_count INTEGER NOT NULL DEFAULT 0,
  overlap_from_sequence INTEGER NOT NULL DEFAULT 0,
  overlap_message_count INTEGER NOT NULL DEFAULT 0,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS conversation_segments_user_conversation_index_idx
  ON conversation_segments (user_id, conversation_id, segment_index);

CREATE INDEX IF NOT EXISTS conversation_segments_user_conversation_range_idx
  ON conversation_segments (user_id, conversation_id, start_sequence, end_sequence);