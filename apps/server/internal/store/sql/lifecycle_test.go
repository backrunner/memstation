package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/store/deletionjournal"

	"github.com/google/uuid"
)

func isolatedIngestionSchema(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL required")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "lifecycle_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		if err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema+",public")
	parsed.RawQuery = query.Encode()
	t.Setenv("MEMSTATION_TEST_DATABASE_URL", parsed.String())
	isolated, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("../../../ent/migrate/migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("migrations missing")
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = isolated.Exec(string(raw)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	_ = isolated.Close()
}

func lifecycleFixture(t *testing.T) (*sql.DB, *MemoryLifecycleRepository, *PostgresIngestionRepository, context.Context, domain.ObserveBatch) {
	db, ingest, ctx, b := ingestionFixture(t)
	repo := NewMemoryLifecycleRepository(runtimeconfig.NewStaticDBHandle(db))
	journal, err := deletionjournal.New(filepath.Join(t.TempDir(), "journal.jsonl"), "test-only-key")
	if err != nil {
		t.Fatal(err)
	}
	repo.SetDeletionJournal(journal)
	ingest.SetDeletionJournal(journal)
	if err = repo.EnsureDeletionJournal(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, repo, ingest, ctx, b
}

func TestLifecycleCandidateCASAndPublicationRevocation(t *testing.T) {
	db, repo, _, ctx, b := lifecycleFixture(t)
	admin := security.WithAdmin(ctx, true)
	candidate, err := repo.Propose(ctx, domain.MemoryCandidate{Kind: "fact", Content: "Approved human fact", Origin: "human"})
	if err != nil {
		t.Fatal(err)
	}
	// Pending candidates are not ordinary records.
	if docs, err := NewPostgresMemoryRepository(db).ListDocuments(ctx, security.UserIDFromContext(ctx), nil, 20); err != nil || len(docs) != 0 {
		t.Fatalf("pending candidate leaked %+v %v", docs, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := repo.Decide(admin, domain.CandidateDecision{ID: candidate.ID, ExpectedRevision: 1, Decision: "approve"})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		if err == nil {
			won++
		} else if !errors.Is(err, domain.ErrAccessConflict) {
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatalf("CAS approvals %d", won)
	}
	items, err := repo.Candidates(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.Inspect(ctx, items[0].ResultNodeID)
	if err != nil {
		t.Fatal(err)
	}
	target := b.WorkspaceID + "-target"
	owner := security.UserIDFromContext(ctx)
	if _, err = db.Exec(`INSERT INTO workspaces(id,user_id,name,kind,capabilities) VALUES($1,$2,'target','group','[]')`, target, owner); err != nil {
		t.Fatal(err)
	}
	published, err := repo.Publish(admin, domain.PublicationInput{SourceNodeID: source.NodeID, SourceRevision: source.Revision, TargetWorkspaceID: target})
	if err != nil {
		t.Fatal(err)
	}
	targetCtx, err := security.BindAdministrativeWorkspace(admin, owner, target)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := repo.Inspect(targetCtx, published.TargetNodeID)
	if err != nil || copy.Content != source.Content || len(copy.SourceRefs) > 0 {
		t.Fatalf("publication %+v %v", copy, err)
	}
	if _, err = repo.Publish(targetCtx, domain.PublicationInput{SourceNodeID: copy.NodeID, SourceRevision: copy.Revision, TargetWorkspaceID: b.WorkspaceID}); !errors.Is(err, domain.ErrAccessConflict) {
		t.Fatalf("publication propagated %v", err)
	}
	if _, err = db.Exec(`UPDATE memory_nodes SET revision=revision+1 WHERE id=$1`, source.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Inspect(targetCtx, published.TargetNodeID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("stale publication remained readable %v", err)
	}
}

func TestLifecycleEraseAndOldBackupReplay(t *testing.T) {
	db, repo, ingest, ctx, b := lifecycleFixture(t)
	receipt, err := ingest.Observe(ctx, b, "source", 8192)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.SourceRef{WorkspaceID: b.WorkspaceID, MessageID: receipt.Events[0].MessageID, Revision: 1, PartIndex: 0}
	candidate, err := repo.Propose(ctx, domain.MemoryCandidate{Kind: "fact", Content: "Sensitive derived fact", Origin: "human", SourceRefs: []domain.SourceRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err = repo.Decide(security.WithAdmin(ctx, true), domain.CandidateDecision{ID: candidate.ID, ExpectedRevision: 1, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := repo.Forget(ctx, domain.ForgetSelector{SourceRefs: []domain.SourceRef{ref}}, "erase")
	if err != nil {
		t.Fatal(err)
	}
	if !operation.ReadBlocked || !operation.JournalPersisted || operation.OnlineCleaned {
		t.Fatalf("untruthful erase status %+v", operation)
	}
	if _, err = repo.Inspect(ctx, candidate.ResultNodeID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("derived memory survived %v", err)
	}
	if _, err = ingest.Source(ctx, ref.MessageID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("source survived %v", err)
	}
	if err = repo.ProcessOperation(context.Background(), operation.ID); err != nil {
		t.Fatal(err)
	}
	current, err := repo.Operation(ctx, operation.ID)
	if err != nil || !current.OnlineCleaned || current.Status != "complete" {
		t.Fatalf("cleanup %+v %v", current, err)
	}
	var body string
	if err = db.QueryRow(`SELECT content FROM facts WHERE node_id=$1`, candidate.ResultNodeID).Scan(&body); err != nil || body != "" {
		t.Fatalf("body retained %q %v", body, err)
	}
	// Simulate an older SQL backup while keeping the independently durable journal.
	if _, err = db.Exec(`DELETE FROM deletion_journal_entries; UPDATE deletion_journal_state SET sequence_num=0,digest=''`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE source_messages SET tombstoned=false,content_status='active',parts='[{"type":"text","text":"restored old secret"}]' WHERE id=$1`, ref.MessageID); err != nil {
		t.Fatal(err)
	}
	if err = repo.EnsureDeletionJournal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Source(ctx, ref.MessageID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("old backup resurrected source %v", err)
	}
}

func TestLifecycleRetractionRepairsSurvivorsAndBlocksQuotedCopies(t *testing.T) {
	db, repo, ingest, ctx, b := lifecycleFixture(t)
	first, err := ingest.Observe(ctx, b, "original", 8192)
	if err != nil {
		t.Fatal(err)
	}
	original := b.Events[0]
	quoted := original
	quoted.EventID = "quoted"
	quoted.SourceMessageID = "quoted"
	quoted.Speaker.ExternalID = "bob"
	quoted.Parts = []domain.SourcePart{{Type: "quote", Text: original.Parts[0].Text, Source: &domain.SourceRef{WorkspaceID: b.WorkspaceID, MessageID: first.Events[0].MessageID, Revision: 1, PartIndex: 0}}}
	survivor := original
	survivor.EventID = "survivor"
	survivor.SourceMessageID = "survivor"
	survivor.Speaker.ExternalID = "carol"
	survivor.Parts = []domain.SourcePart{{Type: "text", Text: "下周三到公司。"}}
	b.Events = []domain.SourceEvent{quoted, survivor}
	second, err := ingest.Observe(ctx, b, "more", 8192)
	if err != nil {
		t.Fatal(err)
	}
	removed := original
	removed.EventID = "retract"
	removed.Type = "message.deleted"
	removed.SourceRevision = 2
	removed.Parts = nil
	b.Events = []domain.SourceEvent{removed}
	receipt, err := ingest.Observe(ctx, b, "remove", 8192)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.IndexedSequence != 0 {
		t.Fatalf("erase or repair skipped in watermark: %+v", receipt)
	}
	for _, id := range []string{first.Events[0].MessageID, second.Events[0].MessageID} {
		if _, err = ingest.Source(ctx, id); !errors.Is(err, domain.ErrAccessNotFound) {
			t.Fatalf("retracted content accessible %s %v", id, err)
		}
	}
	if _, err = ingest.Source(ctx, second.Events[1].MessageID); err != nil {
		t.Fatalf("unrelated source removed %v", err)
	}
	var repair, op string
	if err = db.QueryRow(`SELECT processing_conversation_id FROM source_events WHERE source_message_id=$1`, second.Events[1].MessageID).Scan(&repair); err != nil {
		t.Fatal(err)
	}
	if repair == "" || repair == first.Events[0].ConversationID {
		t.Fatalf("survivor not moved to new sealed repair window %q", repair)
	}
	if err = db.QueryRow(`SELECT operation_id FROM source_events WHERE source_message_id=$1 ORDER BY sequence_num DESC LIMIT 1`, first.Events[0].MessageID).Scan(&op); err != nil {
		t.Fatal(err)
	}
	if err = repo.ProcessOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	progress, err := ingest.Progress(ctx, receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.IndexedSequence != 2 {
		t.Fatalf("watermark crossed unfinished surviving message %+v", progress)
	}
	if _, err = db.Exec(`UPDATE conversations SET finalize_status='ended',summary_status='completed',analysis_status='completed' WHERE id=$1`, repair); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO memory_jobs(id,user_id,workspace_id,conversation_id,task_type,status,attempt) VALUES($1,$2,$3,$4,'keymemory.reconcile','complete',0)`, uuid.NewString(), security.UserIDFromContext(ctx), b.WorkspaceID, repair); err != nil {
		t.Fatal(err)
	}
	progress, err = ingest.Progress(ctx, receipt.ID)
	if err != nil || progress.IndexedSequence != 4 {
		t.Fatalf("repaired prefix %+v %v", progress, err)
	}
}

func TestLifecycleActorEraseBlocksPreviouslyUnseenLateEvent(t *testing.T) {
	db, repo, ingest, ctx, b := lifecycleFixture(t)
	receipt, err := ingest.Observe(ctx, b, "original", 8192)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ingest.Source(ctx, receipt.Events[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := repo.Forget(ctx, domain.ForgetSelector{ActorID: source.SpeakerActorID}, "forget-actor")
	if err != nil {
		t.Fatal(err)
	}
	if !operation.ReadBlocked {
		t.Fatal("erase did not block reads")
	}
	if err = repo.ProcessOperation(context.Background(), operation.ID); err != nil {
		t.Fatal(err)
	}
	var name string
	if err = db.QueryRow(`SELECT display_name FROM workspace_members WHERE workspace_id=$1 AND actor_id=$2`, b.WorkspaceID, source.SpeakerActorID).Scan(&name); err != nil || name != "" {
		t.Fatalf("erased profile retained: %q %v", name, err)
	}
	var identities int
	if err = db.QueryRow(`SELECT COUNT(*) FROM actor_identities WHERE actor_id=$1`, source.SpeakerActorID).Scan(&identities); err != nil || identities != 0 {
		t.Fatalf("erased external identity retained: %d %v", identities, err)
	}
	e := b.Events[0]
	e.EventID = "late-unseen"
	e.SourceMessageID = "late-unseen"
	b.Events = []domain.SourceEvent{e}
	late, err := ingest.Observe(ctx, b, "late", 8192)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Source(ctx, late.Events[0].MessageID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("late actor event resurrected data %v", err)
	}
}

func TestPublicationRetractionIsDurableAcrossBackupRestore(t *testing.T) {
	db, repo, _, ctx, b := lifecycleFixture(t)
	admin := security.WithAdmin(ctx, true)
	candidate, err := repo.Propose(ctx, domain.MemoryCandidate{Kind: "fact", Content: "Explicitly approved copy", Origin: "human"})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := repo.Decide(admin, domain.CandidateDecision{ID: candidate.ID, ExpectedRevision: 1, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.Inspect(ctx, approved.ResultNodeID)
	if err != nil {
		t.Fatal(err)
	}
	target := b.WorkspaceID + "-publication-target"
	user := security.UserIDFromContext(ctx)
	if _, err = db.Exec(`INSERT INTO workspaces(id,user_id,name,kind,capabilities) VALUES($1,$2,'target','group','[]')`, target, user); err != nil {
		t.Fatal(err)
	}
	publication, err := repo.Publish(admin, domain.PublicationInput{SourceNodeID: source.NodeID, SourceRevision: source.Revision, TargetWorkspaceID: target})
	if err != nil {
		t.Fatal(err)
	}
	retracted, err := repo.RetractPublication(admin, publication.ID, publication.Revision)
	if err != nil || retracted.OperationID == "" {
		t.Fatalf("untracked retraction %+v %v", retracted, err)
	}
	targetCtx, err := security.BindAdministrativeWorkspace(admin, user, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Inspect(targetCtx, publication.TargetNodeID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("copy remains readable %v", err)
	}
	if _, err = db.Exec(`DELETE FROM deletion_journal_entries; UPDATE deletion_journal_state SET sequence_num=0,digest=''`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE memory_nodes SET access_status='active' WHERE id=$1`, publication.TargetNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE memory_publications SET status='active',revision=1 WHERE id=$1`, publication.ID); err != nil {
		t.Fatal(err)
	}
	// The first authenticated request can trigger recovery even when its scope
	// differs from a publication target stored in the independent journal.
	if err = NewMemoryGate(repo).Check(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Inspect(targetCtx, publication.TargetNodeID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("backup resurrected publication %v", err)
	}
}

func TestDeletionRecoveryRefusesMissingHistoryOnPopulatedDatabase(t *testing.T) {
	db, repo, ingest, ctx, b := lifecycleFixture(t)
	if _, err := ingest.Observe(ctx, b, "populate", 8192); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM deletion_journal_state`); err != nil {
		t.Fatal(err)
	}
	missing, err := deletionjournal.New(filepath.Join(t.TempDir(), "missing.jsonl"), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	repo.SetDeletionJournal(missing)
	if err = repo.EnsureDeletionJournal(ctx); err == nil {
		t.Fatal("restored data served without deletion history")
	}
	if err = repo.InitializeLegacyDeletionJournal(ctx); err != nil {
		t.Fatal(err)
	}
	// A registered deployment cannot be reset, even with the explicit upgrade flag.
	replacement, err := deletionjournal.New(filepath.Join(t.TempDir(), "replacement.jsonl"), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	repo.SetDeletionJournal(replacement)
	if err = repo.InitializeLegacyDeletionJournal(ctx); err == nil {
		t.Fatal("registered journal replaced")
	}
}

func TestMemoryControlScopesAndReplaysOriginalJob(t *testing.T) {
	db, repo, ingest, ctx, b := lifecycleFixture(t)
	receipt, err := ingest.Observe(ctx, b, "control", 8192)
	if err != nil {
		t.Fatal(err)
	}
	admin := security.WithAdmin(ctx, true)
	user := security.UserIDFromContext(ctx)
	if _, err = db.Exec(`INSERT INTO memory_jobs(id,user_id,workspace_id,conversation_id,task_type,status,queue,generation_id) VALUES('failed-job',$1,$2,$3,'summary.generate','dead_letter','low','generation')`, user, b.WorkspaceID, receipt.Events[0].ConversationID); err != nil {
		t.Fatal(err)
	}
	overview, err := repo.Overview(admin, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Actors) != 1 || len(overview.Sources) != 1 || len(overview.Jobs) != 1 || overview.Jobs[0].GenerationID != "generation" {
		t.Fatalf("overview %+v", overview)
	}
	calls := 0
	if err = repo.ReplayJob(admin, "failed-job", func(queue, id string) error {
		calls++
		if queue != "low" || id != "failed-job" {
			t.Fatalf("wrong replay %s %s", queue, id)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("replays %d", calls)
	}
	if err = repo.ReplayJob(admin, "failed-job", func(string, string) error { t.Fatal("replayed running job"); return nil }); !errors.Is(err, domain.ErrAccessConflict) {
		t.Fatal(err)
	}
	if _, err = repo.Overview(ctx, "", 50); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("runtime read administration %v", err)
	}
}

func TestExplicitEmptyWindowClosesWithoutModelWork(t *testing.T) {
	db, _, ingest, ctx, b := lifecycleFixture(t)
	w, err := ingest.OpenWindow(ctx, domain.ConversationWindowInput{WorkspaceID: b.WorkspaceID, IntegrationID: b.IntegrationID, ExternalSpaceID: b.ExternalSpaceID}, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if err = ingest.FinalizeWindow(ctx, w.ID, w.Revision); err != nil {
		t.Fatal(err)
	}
	if err = ingest.FinalizeWindow(ctx, w.ID, w.Revision); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id=$1`, w.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("empty window queued %d %v", count, err)
	}
	final, err := ingest.Window(ctx, w.ID)
	if err != nil || final.Status != "ended" {
		t.Fatalf("window %+v %v", final, err)
	}
}

func TestLifecycleListsPageWithoutDroppingIndependentCollections(t *testing.T) {
	db, repo, ingest, ctx, b := lifecycleFixture(t)
	for i := 0; i < 3; i++ {
		b.Events[0].EventID = fmt.Sprintf("page-event-%d", i)
		b.Events[0].SourceMessageID = b.Events[0].EventID
		if _, err := ingest.Observe(ctx, b, b.Events[0].EventID, 8192); err != nil {
			t.Fatal(err)
		}
	}
	admin := security.WithAdmin(ctx, true)
	after := ""
	seen := map[string]bool{}
	actors := 0
	for page := 0; page < 10; page++ {
		out, err := repo.Overview(admin, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		actors += len(out.Actors)
		for _, source := range out.Sources {
			if seen[source.ID] {
				t.Fatal("duplicated source across pages")
			}
			seen[source.ID] = true
		}
		after = out.NextCursor
		if after == "" {
			break
		}
	}
	if len(seen) != 3 || actors != 1 || after != "" {
		t.Fatalf("pagination lost independent positions: sources=%d actors=%d", len(seen), actors)
	}
	for i := 0; i < 103; i++ {
		_, err := db.Exec(`INSERT INTO memory_candidates(id,user_id,workspace_id,agent_id,kind,content,origin) VALUES($1,$2,$3,'test','fact','page fixture','human')`, fmt.Sprintf("page-%03d", i), security.UserIDFromContext(ctx), b.WorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := repo.Candidates(ctx, "")
	if err != nil || len(first) != 100 {
		t.Fatalf("first candidate page %d %v", len(first), err)
	}
	second, err := repo.Candidates(ctx, "", first[99].ID)
	if err != nil || len(second) != 3 || second[0].ID == first[99].ID {
		t.Fatalf("next candidate page %d %v", len(second), err)
	}
}

func TestExportRevalidatesAndExpiresStoredBodies(t *testing.T) {
	db, repo, _, ctx, _ := lifecycleFixture(t)
	candidate, err := repo.Propose(ctx, domain.MemoryCandidate{Kind: "fact", Content: "Export fixture", Origin: "human"})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := repo.Decide(security.WithAdmin(ctx, true), domain.CandidateDecision{ID: candidate.ID, ExpectedRevision: 1, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := repo.Export(ctx, "before-correction")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ProcessOperation(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
	ready, err := repo.Operation(ctx, op.ID)
	if err != nil || ready.Status != "complete" {
		t.Fatalf("export incomplete %+v %v", ready, err)
	}
	if _, err = db.Exec(`UPDATE memory_nodes SET revision=revision+1 WHERE id=$1`, approved.ResultNodeID); err != nil {
		t.Fatal(err)
	}
	stale, err := repo.Operation(ctx, op.ID)
	if err != nil || stale.Status != "invalidated" || stale.Result != nil {
		t.Fatalf("stale export exposed %+v %v", stale, err)
	}
	if _, err = db.Exec(`UPDATE memory_operations SET result='{"secret":"expired"}',status='complete',expires_at=$2 WHERE id=$1`, op.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = repo.ExpireExports(context.Background()); err != nil {
		t.Fatal(err)
	}
	var body string
	if err = db.QueryRow(`SELECT result::text FROM memory_operations WHERE id=$1`, op.ID).Scan(&body); err != nil || body != "{}" {
		t.Fatalf("expired export retained %q %v", body, err)
	}
}

func TestInspectReturnsBodyAndRevisionFromSameSnapshot(t *testing.T) {
	db, repo, _, ctx, _ := lifecycleFixture(t)
	candidate, err := repo.Propose(ctx, domain.MemoryCandidate{Kind: "fact", Content: "revision-1", Origin: "human"})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := repo.Decide(security.WithAdmin(ctx, true), domain.CandidateDecision{ID: candidate.ID, ExpectedRevision: 1, Decision: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for revision := 2; revision <= 60; revision++ {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				done <- err
				return
			}
			if _, err = tx.ExecContext(ctx, `UPDATE facts SET content=$2 WHERE node_id=$1`, approved.ResultNodeID, fmt.Sprintf("revision-%d", revision)); err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE memory_nodes SET revision=$2 WHERE id=$1`, approved.ResultNodeID, revision)
			}
			if err != nil {
				_ = tx.Rollback()
				done <- err
				return
			}
			if err = tx.Commit(); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	var mismatch string
	for i := 0; i < 100; i++ {
		doc, err := repo.Inspect(ctx, approved.ResultNodeID)
		if err != nil {
			mismatch = err.Error()
			break
		}
		if doc.Content != fmt.Sprintf("revision-%d", doc.Revision) {
			mismatch = fmt.Sprintf("mixed body/revision: %q revision=%d", doc.Content, doc.Revision)
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if mismatch != "" {
		t.Fatal(mismatch)
	}
}

func TestActorOverviewDoesNotLeakPrivateDisplayName(t *testing.T) {
	db, repo, ingest, ctx, batch := lifecycleFixture(t)
	accepted, err := ingest.Observe(ctx, batch, "public-name", 8192)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ingest.Source(ctx, accepted.Events[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	access, _ := security.AccessFromContext(ctx)
	partition := "private-name-test"
	if _, err = db.ExecContext(ctx, `INSERT INTO memory_partitions(id,user_id,workspace_id,agent_id,audience_actor_ids) VALUES($1,$2,$3,$4,$5)`, partition, access.UserID, access.WorkspaceID, access.AgentID, marshalJSON([]string{source.SpeakerActorID})); err != nil {
		t.Fatal(err)
	}
	privateAccess := access
	privateAccess.PartitionIDs = []string{partition}
	privateCtx := security.WithAccess(ctx, privateAccess)
	private := batch
	private.PartitionID = partition
	private.Events = append([]domain.SourceEvent(nil), batch.Events...)
	private.Events[0].EventID = "private-name"
	private.Events[0].SourceMessageID = "private-name"
	private.Events[0].Speaker.DisplayName = "Private nickname"
	private.Events[0].OccurredAt = private.Events[0].OccurredAt.Add(time.Second)
	if _, err = ingest.Observe(privateCtx, private, "private-name", 8192); err != nil {
		t.Fatal(err)
	}
	public, err := repo.Overview(security.WithAdmin(ctx, true), "", 100)
	if err != nil || len(public.Actors) != 1 || public.Actors[0].DisplayName != batch.Events[0].Speaker.DisplayName {
		t.Fatalf("private nickname leaked to public overview: %+v %v", public.Actors, err)
	}
	privateView, err := repo.Overview(security.WithAdmin(privateCtx, true), "", 100)
	if err != nil || len(privateView.Actors) != 1 || privateView.Actors[0].DisplayName != "Private nickname" {
		t.Fatalf("private overview lost its own nickname: %+v %v", privateView.Actors, err)
	}
}
