package conversation

import (
	"context"
	"encoding/json"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
)

// A checkpoint must hold the extractor's whole-conversation observations.
const maxProcessingCheckpointBytes = 4 << 20

func checkpointKey(identity string) string { return "_processing_checkpoint_" + identity }
func checkProcessingCheckpoint(identity string, value json.RawMessage) error {
	if len(identity) != 64 || len(value) > maxProcessingCheckpointBytes || len(value) > 0 && !json.Valid(value) {
		return errors.New("invalid processing checkpoint")
	}
	return nil
}

func (r *HybridConversationRepository) ProcessingCheckpoint(ctx context.Context, user, id string, revision int64, identity string, value json.RawMessage) (json.RawMessage, error) {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).ProcessingCheckpoint(ctx, user, id, revision, identity, value)
	}
	return r.fallback.ProcessingCheckpoint(ctx, user, id, revision, identity, value)
}

func (r *PostgresConversationRepository) ProcessingCheckpoint(ctx context.Context, user, id string, revision int64, identity string, value json.RawMessage) (json.RawMessage, error) {
	if err := checkProcessingCheckpoint(identity, value); err != nil {
		return nil, err
	}
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil {
		return nil, err
	}
	conversation, err := r.GetConversation(ctx, user, id)
	if err != nil {
		return nil, err
	}
	if conversation.Revision != revision {
		return nil, domain.ErrMemoryTaskObsolete
	}
	var raw []byte
	key := checkpointKey(identity)
	if len(value) == 0 {
		err = r.db.QueryRowContext(ctx, `SELECT COALESCE(metadata->$5,'null'::jsonb) FROM conversations WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND write_revision=$4`, id, user, workspace, revision, key).Scan(&raw)
	} else {
		err = r.db.QueryRowContext(ctx, `UPDATE conversations SET metadata=COALESCE(metadata,'{}'::jsonb)||jsonb_build_object($5::text,COALESCE(metadata->$5,$6::jsonb)) WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND write_revision=$4 RETURNING metadata->$5`, id, user, workspace, revision, key, []byte(value)).Scan(&raw)
	}
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	return raw, nil
}

func (r *InMemoryConversationRepository) ProcessingCheckpoint(ctx context.Context, user, id string, revision int64, identity string, value json.RawMessage) (json.RawMessage, error) {
	if err := checkProcessingCheckpoint(identity, value); err != nil {
		return nil, err
	}
	conversation, err := r.GetConversation(ctx, user, id)
	if err != nil {
		return nil, err
	}
	if conversation.Revision != revision {
		return nil, domain.ErrMemoryTaskObsolete
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, c := range r.conversations {
		if c.UserID != user || c.ConversationID != id || c.WorkspaceID != conversation.WorkspaceID {
			continue
		}
		if c.Revision != revision {
			return nil, domain.ErrMemoryTaskObsolete
		}
		k := checkpointKey(identity)
		if v, ok := c.Metadata[k]; ok {
			return json.Marshal(v)
		}
		if len(value) == 0 {
			return nil, nil
		}
		var v any
		if err := json.Unmarshal(value, &v); err != nil {
			return nil, err
		}
		if c.Metadata == nil {
			c.Metadata = map[string]any{}
		}
		copyMetadata := make(map[string]any, len(c.Metadata)+1)
		for name, item := range c.Metadata {
			copyMetadata[name] = item
		}
		c.Metadata = copyMetadata
		c.Metadata[k] = v
		r.conversations[key] = c
		return append(json.RawMessage{}, value...), nil
	}
	return nil, domain.ErrConversationNotFound
}
