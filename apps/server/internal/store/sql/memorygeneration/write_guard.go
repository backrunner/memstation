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
	if _, err := security.PersonalScope(ctx, user, workspace); err != nil {
		return false, err
	}
	if generation == "" {
		return true, nil
	}
	var owner, scope, status string
	err := tx.QueryRowContext(ctx, `SELECT user_id,workspace_id,status FROM memory_generations WHERE generation_id=$1 FOR SHARE`, generation).Scan(&owner, &scope, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if owner != user || scope != domain.PersonalWorkspaceID(user) {
		return false, domain.ErrAccessDenied
	}
	return status != string(domain.MemoryGenerationStatusDiscarded), nil
}
