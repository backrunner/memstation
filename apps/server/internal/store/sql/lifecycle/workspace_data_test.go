package lifecycle

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	identitystore "memstation/apps/server/internal/store/sql/identity"
	testfixturestore "memstation/apps/server/internal/store/sql/internal/testfixture"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"github.com/google/uuid"
)

func TestWorkspaceMemoryDataIsolation(t *testing.T) {
	t.Run("memory", func(t *testing.T) { workspaceDataSuite(t, memorystore.NewInMemoryMemoryRepository(), nil) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
		if dsn == "" {
			t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		workspaceDataSuite(t, memorystore.NewPostgresMemoryRepository(db), db)
	})
}

func workspaceDataSuite(t *testing.T, repo ports.MemoryRepository, db *sql.DB) {
	t.Helper()
	base := context.Background()
	owner := "multi-" + uuid.NewString()
	a, b := owner+"-a", owner+"-b"
	private := owner + "-private"
	if db != nil {
		ids := identitystore.NewIdentityRepository(runtimeconfig.NewStaticDBHandle(db))
		testfixturestore.ProvisionFixtureAgent(t, ids, owner)
		if _, err := ids.EnsurePersonal(base, owner); err != nil {
			t.Fatal(err)
		}
		for _, workspace := range []string{a, b} {
			if _, err := db.Exec(`INSERT INTO workspaces(id,user_id,kind,name,capabilities) VALUES($1,$2,'group','Group','[]')`, workspace, owner); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO memory_partitions(id,user_id,workspace_id,agent_id,audience_actor_ids) VALUES($1,$2,$3,$4,'["member"]')`, private, owner, a, testfixturestore.FixtureAgentID(owner)); err != nil {
			t.Fatal(err)
		}
	}
	ca, _ := security.WithDataScope(base, owner, a)
	cb, _ := security.WithDataScope(base, owner, b)
	cp, _ := security.WithDataScope(base, owner, a, private)
	for i, ctx := range []context.Context{ca, cb, cp} {
		testfixturestore.ProvisionGeneration(t, db, ctx, owner, "")
		content := []string{"group a", "group b", "private a"}[i]
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{UserID: owner, Type: domain.MemoryTypeFact, Content: content, Metadata: map[string]any{"fact_key": "same-name"}}); err != nil {
			t.Fatal(i, err)
		}
		if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{UserID: owner, Key: "same-key", Value: content}); err != nil {
			t.Fatal(i, err)
		}
	}
	nodeIDs := map[string]bool{}
	for i, ctx := range []context.Context{ca, cb, cp} {
		expected := []string{"group a", "group b", "private a"}[i]
		docs, err := repo.ListDocuments(ctx, owner, []domain.MemoryType{domain.MemoryTypeFact}, 1)
		if err != nil || len(docs) != 1 || docs[0].Content != expected {
			t.Fatalf("scope %d: %+v %v", i, docs, err)
		}
		if nodeIDs[docs[0].NodeID] {
			t.Fatal("same fact key collided across spaces/partitions")
		}
		nodeIDs[docs[0].NodeID] = true
		keys, err := repo.ListKeyMemories(ctx, owner, 10)
		if err != nil || len(keys) != 1 || keys[0].Value != expected {
			t.Fatalf("keys %d: %+v %v", i, keys, err)
		}
		other := ca
		if i == 0 {
			other = cb
		}
		if got, err := repo.GetDocument(other, owner, docs[0].ID, docs[0].Type); err == nil {
			t.Fatalf("cross-scope ID returned %+v", got)
		}
	}
	docs, err := repo.ListDocuments(base, owner, nil, 20)
	if err != nil || len(docs) != 0 {
		t.Fatalf("legacy scope widened: %+v %v", docs, err)
	}
}
