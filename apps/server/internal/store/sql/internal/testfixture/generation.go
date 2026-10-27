package testfixture

import (
	"context"
	"database/sql"
	"testing"

	"memstation/apps/server/internal/security"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"
)

func ProvisionGeneration(t *testing.T, db *sql.DB, ctx context.Context, user, generation string) {
	t.Helper()
	if db == nil {
		return
	}
	workspace := security.WorkspaceID(ctx, user)
	if generation == "" {
		generation = recordutilstore.CurrentGenerationID(workspace)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,kind,capabilities) VALUES($1,$2,'Fixture','personal','[]') ON CONFLICT DO NOTHING`, workspace, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO memory_generations(generation_id,user_id,workspace_id,status,is_active) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, generation, user, workspace, func() string {
		if generation == recordutilstore.CurrentGenerationID(workspace) {
			return "activated"
		}
		return "processing"
	}(), generation == recordutilstore.CurrentGenerationID(workspace)); err != nil {
		t.Fatal(err)
	}
}
