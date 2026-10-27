package memory

import (
	"context"
	"testing"

	"memstation/apps/server/internal/ports"
	testfixturestore "memstation/apps/server/internal/store/sql/internal/testfixture"
)

func provisionFixtureGeneration(t *testing.T, repo ports.MemoryRepository, ctx context.Context, user, generation string) {
	t.Helper()
	if pg, ok := repo.(*PostgresMemoryRepository); ok {
		testfixturestore.ProvisionGeneration(t, pg.db, ctx, user, generation)
	}
}
