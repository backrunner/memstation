package conversation

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"os"
	"sync"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
	identitystore "memstation/apps/server/internal/store/sql/identity"
	testfixturestore "memstation/apps/server/internal/store/sql/internal/testfixture"

	"github.com/google/uuid"
)

func TestConversationOwnershipAndRevision(t *testing.T) {
	conversationAccessSuite(t, NewInMemoryConversationRepository())
}

func TestPostgresConversationOwnershipAndRevision(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sqlpkg.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conversationAccessSuite(t, NewPostgresConversationRepository(db))
}

func conversationAccessSuite(t *testing.T, repo ports.ConversationRepository) {
	t.Helper()
	ctx := context.Background()
	id := "access-test-" + uuid.NewString()
	zero := int64(0)
	if pg, ok := repo.(*PostgresConversationRepository); ok {
		for _, u := range []string{"owner-a", "owner-b"} {
			testfixturestore.ProvisionFixtureAgent(t, identitystore.NewIdentityRepository(runtimeconfig.NewStaticDBHandle(pg.db)), u)
		}
	}
	record := domain.ConversationRecord{ConversationID: id, UserID: "owner-a", WorkspaceID: domain.PersonalWorkspaceID("owner-a"), AgentID: testfixturestore.FixtureAgentID("owner-a"), RequestFormat: string(domain.ProtocolOpenAIChat), Mode: domain.ModeTransparent, FinalizeStatus: domain.FinalizeStatusActive, ExpectedRevision: &zero, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "original"}}, Metadata: map[string]any{"agent_id": "agent-a"}}
	if err := repo.PersistExchange(ctx, record); err != nil {
		t.Fatal(err)
	}
	stolen := record
	stolen.UserID = "owner-b"
	stolen.ExpectedRevision = nil
	if err := repo.PersistExchange(ctx, stolen); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("ownership changed: %v", err)
	}
	stolen = record
	stolen.ExpectedRevision = nil
	stolen.AgentID = testfixturestore.FixtureAgentID("owner-b")
	if err := repo.PersistExchange(ctx, stolen); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("agent identity changed: %v", err)
	}
	saved, err := repo.GetConversation(ctx, record.UserID, id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || saved.Messages[0].Text != "original" {
		t.Fatal("ownership check mutated conversation")
	}
	record.ExpectedRevision = &saved.Revision
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, word := range []string{"first", "second"} {
		wg.Go(func() {
			next := record
			next.Metadata = map[string]any{"agent_id": "agent-a"}
			next.Messages = []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "original"}, {Role: domain.RoleAssistant, Text: word}}
			results <- repo.PersistExchange(ctx, next)
		})
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, domain.ErrAccessConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent history overwrite: %d/%d", successes, conflicts)
	}
	saved, err = repo.GetConversation(ctx, record.UserID, id)
	if err != nil || saved.Revision != 2 || len(saved.Messages) != 2 {
		t.Fatalf("bad committed state: %+v %v", saved, err)
	}
}
