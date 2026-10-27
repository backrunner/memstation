package memorygen

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/service/apikey"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"

	"github.com/google/uuid"
)

func TestGenerationOperationsRejectForeignScope(t *testing.T) {
	s := NewService(nil, "fixture")
	for _, access := range []domain.AccessContext{{UserID: "other", WorkspaceID: domain.PersonalWorkspaceID("other")}, {UserID: "owner", WorkspaceID: "group"}} {
		ctx := security.WithAccess(context.Background(), access)
		calls := map[string]func() error{
			"active":  func() error { _, e := s.EnsureActiveGeneration(ctx, "owner"); return e },
			"resolve": func() error { _, e := s.ResolveGeneration(ctx, "owner", "gen"); return e },
			"pending": func() error { _, _, e := s.GetInProgressGeneration(ctx, "owner"); return e },
			"get":     func() error { _, e := s.GetGeneration(ctx, "owner", "gen"); return e },
			"list":    func() error { _, e := s.ListGenerations(ctx, "owner", 1); return e },
			"sources": func() error { _, e := s.ListGenerationSources(ctx, "owner", "gen"); return e },
			"current": func() error { _, e := s.IsCurrentGenerationSource(ctx, "owner", "gen", "snap"); return e },
			"capture": func() error {
				_, _, e := s.CaptureSourceSnapshot(ctx, domain.ConversationRecord{UserID: "owner", ConversationID: "conv", AgentID: "fixture-agent"})
				return e
			},
			"snapshot":  func() error { _, _, e := s.GetSourceSnapshot(ctx, "owner", "snap"); return e },
			"latest":    func() error { _, _, e := s.GetLatestSourceSnapshot(ctx, "owner", "conv"); return e },
			"snapshots": func() error { _, e := s.ListSourceSnapshots(ctx, "owner", "conv", 1); return e },
			"stats":     func() error { _, e := s.SourceStatsForUser(ctx, "owner"); return e },
			"rebuild":   func() error { _, _, e := s.CreateRebuildGeneration(ctx, CreateRebuildInput{UserID: "owner"}); return e },
			"add": func() error {
				_, _, e := s.AddSnapshotToGeneration(ctx, "gen", domain.MemorySourceSnapshot{UserID: "owner", SnapshotID: "snap"})
				return e
			},
			"running":      func() error { return s.MarkGenerationSourceRunning(ctx, "owner", "gen", "snap") },
			"complete":     func() error { _, _, e := s.MarkGenerationSourceCompleted(ctx, "owner", "gen", "snap", 1); return e },
			"fail":         func() error { _, e := s.MarkGenerationSourceFailed(ctx, "owner", "gen", "snap", "failure"); return e },
			"activate":     func() error { _, e := s.ActivateGeneration(ctx, "owner", "gen"); return e },
			"autoActivate": func() error { _, e := s.ActivateCompletedGeneration(ctx, "owner", "gen"); return e },
			"discard":      func() error { _, e := s.DiscardGeneration(ctx, "owner", "gen"); return e },
		}
		for name, call := range calls {
			if e := call(); !errors.Is(e, domain.ErrAccessDenied) {
				t.Errorf("%s: %v", name, e)
			}
		}
	}
}

func TestSourceOwnerIdentityDoesNotUsePrefixMatching(t *testing.T) {
	s := NewService(nil, "fixture")
	ctx := context.Background()
	for _, owner := range []string{"owner", "owner::nested"} {
		if _, _, err := s.CaptureSourceSnapshot(ctx, domain.ConversationRecord{UserID: owner, ConversationID: "conv", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: owner}}, AgentID: "fixture-agent"}); err != nil {
			t.Fatal(err)
		}
	}
	g, sources, err := s.CreateRebuildGeneration(ctx, CreateRebuildInput{UserID: "owner"})
	if err != nil || len(sources) != 1 || g.SourceCount != 1 {
		t.Fatalf("owner prefix leaked sources: %+v %v", sources, err)
	}
	if _, _, err = s.CaptureSourceSnapshot(ctx, domain.ConversationRecord{UserID: "owner", ConversationID: "forged", Parts: []domain.MessagePart{{UserID: "other"}}, AgentID: "fixture-agent"}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("forged nested source accepted", err)
	}
}

func TestPostgresScopedGenerationLifecycle(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner := "generation-scope-" + uuid.NewString()
	workspace := domain.PersonalWorkspaceID(owner)
	group := owner + "-group"
	hiddenGen := owner + "-hidden-gen"
	hiddenSnap := owner + "-hidden-snap"
	agent, agentErr := apikey.NewService(runtimeconfig.NewStaticDBHandle(db)).Identities().Create(context.Background(), owner, "Generation fixture", nil)
	if agentErr != nil {
		t.Fatal(agentErr)
	}
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: owner, AgentID: agent.ID, WorkspaceID: workspace, ScopeVersion: 2, PartitionIDs: []string{""}})
	s := NewService(runtimeconfig.NewStaticDBHandle(db), "fixture")
	active, err := s.EnsureActiveGeneration(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO workspaces(id,user_id,name,kind,capabilities) VALUES($1,$2,'Group','group','[]')`, []any{group, owner}},
		{`INSERT INTO memory_generations(generation_id,user_id,workspace_id,status,is_active) VALUES($1,$2,$3,'activated',true)`, []any{hiddenGen, owner, group}},
		{`INSERT INTO memory_source_snapshots(snapshot_id,user_id,workspace_id,conversation_id,snapshot_version,source_hash,request_format) VALUES($1,$2,$3,'hidden-conversation',1,'hidden','openai_chat')`, []any{hiddenSnap, owner, group}},
	} {
		if _, err = db.ExecContext(ctx, statement.q, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := s.ListGenerations(ctx, owner, 1)
	if err != nil || len(listed) != 1 || listed[0].GenerationID != active.GenerationID {
		t.Fatalf("hidden generation displaced visible result: %+v %v", listed, err)
	}
	if _, err = s.GetGeneration(ctx, owner, hiddenGen); err == nil {
		t.Fatal("hidden generation directly readable")
	}
	if _, ok, err := s.GetSourceSnapshot(ctx, owner, hiddenSnap); err != nil || ok {
		t.Fatal("hidden snapshot directly readable", err)
	}
	conversation := domain.ConversationRecord{UserID: owner, WorkspaceID: workspace, ConversationID: owner + "-conv", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "a retained source"}}, AgentID: agent.ID}
	conversations := conversationstore.NewPostgresConversationRepository(db)
	if err = conversations.PersistExchange(ctx, conversation); err != nil {
		t.Fatal(err)
	}
	var created atomic.Int32
	parallelCalls(t, 16, func() error {
		_, fresh, e := s.CaptureSourceSnapshot(ctx, conversation)
		if fresh {
			created.Add(1)
		}
		return e
	})
	if created.Load() != 1 {
		t.Fatalf("duplicate source captured %d times", created.Load())
	}
	snap, ok, err := s.GetLatestSourceSnapshot(ctx, owner, conversation.ConversationID)
	if err != nil || !ok || len(snap.Messages) != 1 || snap.WorkspaceID != workspace {
		t.Fatalf("source roundtrip: %+v %v", snap, err)
	}
	stats, err := s.SourceStatsForUser(ctx, owner)
	if err != nil || stats.SnapshotCount != 1 {
		t.Fatal("hidden source counted", stats, err)
	}
	var successes atomic.Int32
	var candidates sync.Map
	parallelCalls(t, 16, func() error {
		g, _, e := s.CreateRebuildGeneration(ctx, CreateRebuildInput{UserID: owner, ActivateOnSuccess: true})
		if e == nil {
			successes.Add(1)
			candidates.Store("g", g)
		}
		return nil
	})
	if successes.Load() != 1 {
		t.Fatalf("concurrent rebuild count: %d", successes.Load())
	}
	raw, _ := candidates.Load("g")
	candidate := raw.(domain.MemoryGeneration)
	conversation.ConversationID += "-second"
	second, _, err := s.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	var additions atomic.Int32
	parallelCalls(t, 16, func() error {
		_, added, e := s.AddSnapshotToGeneration(ctx, candidate.GenerationID, second)
		if added {
			additions.Add(1)
		}
		return e
	})
	if additions.Load() != 1 {
		t.Fatal("source added more than once", additions.Load())
	}

	failed, err := s.MarkGenerationSourceFailed(ctx, owner, candidate.GenerationID, snap.SnapshotID, "temporary")
	if err != nil || failed.FailureCount != 1 {
		t.Fatal(failed, err)
	}
	failed, err = s.MarkGenerationSourceFailed(ctx, owner, candidate.GenerationID, snap.SnapshotID, "temporary")
	if err != nil || failed.FailureCount != 1 {
		t.Fatal("duplicate failure counted", failed, err)
	}
	if err = s.MarkGenerationSourceRunning(ctx, owner, candidate.GenerationID, snap.SnapshotID); err != nil {
		t.Fatal(err)
	}
	parallelCalls(t, 16, func() error {
		_, _, e := s.MarkGenerationSourceCompleted(ctx, owner, candidate.GenerationID, snap.SnapshotID, 2)
		return e
	})
	if _, _, err = s.MarkGenerationSourceCompleted(ctx, owner, candidate.GenerationID, second.SnapshotID, 3); err != nil {
		t.Fatal(err)
	}
	candidate, err = s.GetGeneration(ctx, owner, candidate.GenerationID)
	if err != nil || candidate.ProcessedCount != 2 || candidate.EmbeddingCount != 5 || candidate.FailureCount != 0 {
		t.Fatal("completion was not idempotent", candidate, err)
	}
	if _, ready, err := s.MarkGenerationSourceCompleted(ctx, owner, candidate.GenerationID, second.SnapshotID, 99); err != nil || !ready {
		t.Fatal("activation publication cannot be retried", ready, err)
	}
	if _, err = s.ActivateGeneration(ctx, owner, candidate.GenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActivateCompletedGeneration(ctx, owner, active.GenerationID); err != nil {
		t.Fatal(err)
	}
	current, err := s.EnsureActiveGeneration(ctx, owner)
	if err != nil || current.GenerationID != candidate.GenerationID {
		t.Fatal("late activation rolled back current generation", current, err)
	}
	var hiddenActive bool
	if err = db.QueryRowContext(ctx, `SELECT is_active FROM memory_generations WHERE generation_id=$1`, hiddenGen).Scan(&hiddenActive); err != nil || !hiddenActive {
		t.Fatal("activation changed another workspace", err)
	}
	next, sources, err := s.CreateRebuildGeneration(ctx, CreateRebuildInput{UserID: owner})
	if err != nil {
		t.Fatal(err)
	}
	repo := memorystore.NewPostgresMemoryRepository(db)
	doc := domain.MemoryDocument{UserID: owner, WorkspaceID: workspace, GenerationID: next.GenerationID, ID: owner + "-doc", NodeID: owner + "-node", Type: domain.MemoryTypeFact, Content: "candidate"}
	if err = repo.StoreDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO memory_nodes(id,user_id,workspace_id,generation_id,node_type,entity_id) VALUES($1,$2,$3,$4,'fact',$1)`, owner+"-hidden-node", owner, group, next.GenerationID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DiscardGeneration(ctx, owner, next.GenerationID); err != nil {
		t.Fatal(err)
	}
	if err = repo.StoreDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = conversations.ReplaceSegments(ctx, owner, owner+"-conv", []domain.ConversationSegment{{ID: owner + "-late-segment", UserID: owner, ConversationID: owner + "-conv", GenerationID: next.GenerationID}}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM conversation_segments WHERE generation_id=$1`, next.GenerationID).Scan(&count); err != nil || count != 0 {
		t.Fatal("late segment revived discarded generation", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM memory_nodes WHERE id=$1`, doc.NodeID).Scan(&count); err != nil || count != 0 {
		t.Fatal("late write revived discarded generation", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM memory_nodes WHERE id=$1`, owner+"-hidden-node").Scan(&count); err != nil || count != 1 {
		t.Fatal("discard removed another workspace node", err)
	}
	g, ready, err := s.MarkGenerationSourceCompleted(ctx, owner, next.GenerationID, sources[0].SnapshotID, 1)
	if err != nil || ready || g.Status != domain.MemoryGenerationStatusDiscarded {
		t.Fatal("late completion revived generation", g, err)
	}
	if _, err = s.ActivateGeneration(ctx, owner, next.GenerationID); err == nil {
		t.Fatal("discarded generation activated")
	}
	if _, err = db.ExecContext(ctx, `UPDATE memory_source_snapshots SET message_parts='[{"part_id":"legacy-part"}]' WHERE snapshot_id=$1`, snap.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.GetSourceSnapshot(ctx, owner, snap.SnapshotID); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("unscoped stored snapshot accepted", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE memory_source_snapshots SET message_parts='[{"part_id":"foreign","user_id":"other"}]' WHERE snapshot_id=$1`, snap.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.GetSourceSnapshot(ctx, owner, snap.SnapshotID); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("foreign nested source exposed", err)
	}
}

func parallelCalls(t *testing.T, n int, run func() error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- run() }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
