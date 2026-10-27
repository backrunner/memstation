package memorygeneration

import (
	"context"
	"database/sql"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

// LockWritableGeneration shares the lifecycle lock until the caller commits.
// Every generation must be registered; discarded generations reject late tasks.
func LockWritableGeneration(ctx context.Context, tx *sql.Tx, user, workspace, generation string) (bool, error) {
	if _, err := security.DataScope(ctx, user, workspace); err != nil {
		return false, err
	}
	if generation == "" {
		// Key Memory is intentionally independent of generated memory.
		return true, nil
	}
	var owner, scope, status string
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT user_id,workspace_id,status,is_active FROM memory_generations WHERE generation_id=$1 FOR SHARE`, generation).Scan(&owner, &scope, &status, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, domain.ErrAccessDenied
	}
	if err != nil {
		return false, err
	}
	if owner != user || scope != security.WorkspaceID(ctx, user) {
		return false, domain.ErrAccessDenied
	}
	return status != string(domain.MemoryGenerationStatusDiscarded) && (status != string(domain.MemoryGenerationStatusActivated) || active), nil
}
