package sql

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func (r *HybridMediaRepository) MarkBlobForCleanup(ctx context.Context, user, conversation, blob string) (bool, error) {
	if db := currentDB(r.db); db != nil {
		return NewPostgresMediaRepository(db).MarkBlobForCleanup(ctx, user, conversation, blob)
	}
	return r.fallback.MarkBlobForCleanup(ctx, user, conversation, blob)
}

func (r *PostgresMediaRepository) MarkBlobForCleanup(ctx context.Context, user, conversation, blob string) (bool, error) {
	workspace, err := security.PersonalScope(ctx, user, "")
	if err != nil {
		return false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer rollback(tx)
	var id string
	err = tx.QueryRowContext(ctx, `SELECT blob_id FROM blob_objects WHERE user_id=$1 AND workspace_id=$2 AND blob_id=$3 FOR UPDATE`, user, workspace, blob).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var referenced bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM message_parts WHERE user_id=$1 AND workspace_id=$2 AND blob_id=$3 AND conversation_id<>$4) OR EXISTS(SELECT 1 FROM memory_source_snapshots s CROSS JOIN LATERAL jsonb_array_elements(s.message_parts) part WHERE s.user_id=$1 AND s.workspace_id=$2 AND part->>'blob_id'=$3)`, user, workspace, blob, conversation).Scan(&referenced)
	if err != nil || referenced {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE blob_objects SET deleted_at=COALESCE(deleted_at,now()),updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND blob_id=$3`, user, workspace, blob); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r *InMemoryMediaRepository) MarkBlobForCleanup(ctx context.Context, user, conversation, blob string) (bool, error) {
	workspace, err := security.PersonalScope(ctx, user, "")
	if err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.blobs[key(user, blob)]
	if !ok || b.WorkspaceID != workspace {
		return false, nil
	}
	for _, parts := range r.parts {
		for _, p := range parts {
			if p.UserID == user && p.WorkspaceID == workspace && p.BlobID == blob && p.ConversationID != conversation {
				return false, nil
			}
		}
	}
	now := time.Now().UTC()
	b.DeletedAt = &now
	b.UpdatedAt = now
	r.blobs[key(user, blob)] = b
	return true, nil
}

func validatePartBlobsTx(ctx context.Context, tx *sql.Tx, user, workspace string, parts []domain.MessagePart) error {
	ids := map[string]bool{}
	for _, p := range parts {
		if p.BlobID != "" {
			ids[p.BlobID] = true
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		var found string
		if err := tx.QueryRowContext(ctx, `SELECT blob_id FROM blob_objects WHERE user_id=$1 AND workspace_id=$2 AND blob_id=$3 AND deleted_at IS NULL FOR SHARE`, user, workspace, id).Scan(&found); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrAccessDenied
			}
			return err
		}
	}
	return nil
}
