package ingestfixture

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	testfixturestore "memstation/apps/server/internal/store/sql/internal/testfixture"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/store/deletionjournal"
	identitystore "memstation/apps/server/internal/store/sql/identity"
	ingestionstore "memstation/apps/server/internal/store/sql/ingestion"

	"github.com/google/uuid"
)

func IngestionFixture(t *testing.T) (*sql.DB, *ingestionstore.PostgresIngestionRepository, context.Context, domain.ObserveBatch) {
	t.Helper()
	testfixturestore.IsolatedIngestionSchema(t)
	dsn := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	owner := "ingest-" + uuid.NewString()
	workspace := owner + "-group"
	integration := owner + "-integration"
	agent := testfixturestore.ProvisionFixtureAgent(t, identitystore.NewIdentityRepository(runtimeconfig.NewStaticDBHandle(db)), owner)
	if _, err = identitystore.NewIdentityRepository(runtimeconfig.NewStaticDBHandle(db)).EnsurePersonal(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO workspaces(id,user_id,name,kind,capabilities) VALUES($1,$2,'group','group','[]')`, []any{workspace, owner}},
		{`INSERT INTO integrations(id,user_id,name,kind,identity_namespace_id) VALUES($1,$2,'test','test',$1)`, []any{integration, owner}},
		{`INSERT INTO workspace_sources(id,user_id,workspace_id,integration_id,external_space_id) VALUES($1,$2,$3,$4,'room')`, []any{uuid.NewString(), owner, workspace, integration}},
		{`INSERT INTO integration_agent_grants(id,user_id,workspace_id,integration_id,agent_id,allowed_event_types) VALUES($1,$2,$3,$4,$5,'["message.created","message.edited","message.deleted"]')`, []any{uuid.NewString(), owner, workspace, integration, agent}},
	}
	for _, stmt := range statements {
		if _, err = db.Exec(stmt.q, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: owner, WorkspaceID: workspace, AgentID: agent, ScopeVersion: 2, PartitionIDs: []string{""}, Capabilities: domain.AllCapabilities()})
	testfixturestore.ProvisionGeneration(t, db, ctx, owner, "")
	b := domain.ObserveBatch{WorkspaceID: workspace, IntegrationID: integration, ExternalSpaceID: "room", Events: []domain.SourceEvent{{EventID: "event1", Type: "message.created", SourceMessageID: "message1", SourceRevision: 1, Speaker: domain.SourceSpeaker{Kind: "human", ExternalID: "alice", DisplayName: "同名"}, Parts: []domain.SourcePart{{Type: "text", Text: "我在上海工作。"}}, OccurredAt: time.Now().UTC()}}}
	repo := ingestionstore.NewPostgresIngestionRepository(db)
	journal, err := deletionjournal.New(filepath.Join(t.TempDir(), "ingestion-deletions.jsonl"), "test-only-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = journal.Initialize(ctx, ""); err != nil {
		t.Fatal(err)
	}
	repo.SetDeletionJournal(journal)
	return db, repo, ctx, b
}
