package memorygeneration

import (
	"context"
	"encoding/json"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

// SnapshotCheckpoint stores derived processing state on its immutable source,
// including for retained history that no longer has a live conversation row.
// A checkpoint must hold the extractor's whole-conversation observations.
const maxProcessingCheckpointBytes = 4 << 20

func (r *Repository) SnapshotCheckpoint(ctx context.Context, user, generation, snapshotID, identity string, value json.RawMessage) (json.RawMessage, error) {
	if len(identity) != 64 || len(value) > maxProcessingCheckpointBytes || len(value) > 0 && !json.Valid(value) {
		return nil, errors.New("invalid snapshot processing checkpoint")
	}
	snapshot, ok, err := r.GetSourceSnapshot(ctx, user, snapshotID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.ErrAccessNotFound
	}
	current, err := r.IsCurrentGenerationSource(ctx, user, generation, snapshotID)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, domain.ErrMemoryTaskObsolete
	}
	key := "_processing_checkpoint_" + identity
	if db := r.currentDB(); db != nil {
		var raw []byte
		if len(value) == 0 {
			err = db.QueryRowContext(ctx, `SELECT COALESCE(metadata->$4,'null'::jsonb) FROM memory_source_snapshots WHERE snapshot_id=$1 AND user_id=$2 AND workspace_id=$3 AND content_status='active'`, snapshotID, user, security.WorkspaceID(ctx, user), key).Scan(&raw)
		} else {
			err = db.QueryRowContext(ctx, `UPDATE memory_source_snapshots SET metadata=COALESCE(metadata,'{}'::jsonb)||jsonb_build_object($4::text,COALESCE(metadata->$4,$5::jsonb)) WHERE snapshot_id=$1 AND user_id=$2 AND workspace_id=$3 AND content_status='active' RETURNING metadata->$4`, snapshotID, user, security.WorkspaceID(ctx, user), key, []byte(value)).Scan(&raw)
		}
		if err != nil {
			return nil, err
		}
		if string(raw) == "null" {
			return nil, nil
		}
		return raw, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.sourceSnapshots[snapshotID]
	if !ok || stored.UserID != user || stored.WorkspaceID != snapshot.WorkspaceID {
		return nil, domain.ErrAccessNotFound
	}
	if existing, ok := stored.Metadata[key]; ok {
		return json.Marshal(existing)
	}
	if len(value) == 0 {
		return nil, nil
	}
	var parsed any
	if err = json.Unmarshal(value, &parsed); err != nil {
		return nil, err
	}
	if stored.Metadata == nil {
		stored.Metadata = map[string]any{}
	}
	copyMetadata := make(map[string]any, len(stored.Metadata)+1)
	for name, item := range stored.Metadata {
		copyMetadata[name] = item
	}
	stored.Metadata = copyMetadata
	stored.Metadata[key] = parsed
	r.sourceSnapshots[snapshotID] = stored
	return append(json.RawMessage{}, value...), nil
}
