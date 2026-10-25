package memorygeneration

import (
	"context"
	"database/sql"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

// LockWritableGeneration shares the lifecycle lock until the caller commits.
// Legacy default/fixture generations without registry rows remain compatible;
// a registered discarded generation can never be recreated by a late task.
func LockWritableGeneration(ctx context.Context, tx *sql.Tx, user, workspace, generation string) (bool, error) {
	if _, err := security.DataScope(ctx, user, workspace); err != nil {
		return false, err
	}
	if generation == "" {
		return true, nil
	}
	var owner, scope, status string
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT user_id,workspace_id,status,is_active FROM memory_generations WHERE generation_id=$1 FOR SHARE`, generation).Scan(&owner, &scope, &status, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if owner != user || scope != security.WorkspaceID(ctx, user) {
		return false, domain.ErrAccessDenied
	}
	return status != string(domain.MemoryGenerationStatusDiscarded) && (status != string(domain.MemoryGenerationStatusActivated) || active), nil
}
