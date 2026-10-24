package sql

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	memoryservice "memstation/apps/server/internal/service/memory"

	"github.com/google/uuid"
)

func TestMemoryEdits(t *testing.T) { runMemoryEdits(t, NewInMemoryMemoryRepository(), nil) }
func TestPostgresMemoryEdits(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runMemoryEdits(t, NewPostgresMemoryRepository(db), db)
}

func runMemoryEdits(t *testing.T, repo ports.MemoryRepository, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	owner := "memory-edit-" + uuid.NewString()
	s := memoryservice.NewService(nil, repo)
	doc := domain.MemoryDocument{UserID: owner, ID: uuid.NewString(), Type: domain.MemoryTypeFact, Content: "before", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := repo.StoreDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	doc, err := repo.GetDocument(ctx, owner, doc.ID, doc.Type)
	if err != nil {
		t.Fatal(err)
	}

	for _, kind := range []domain.MemoryType{domain.MemoryTypeSummary, domain.MemoryTypeImpression, domain.MemoryTypeEvent, domain.MemoryTypeTask, domain.MemoryTypeProcedure, domain.MemoryTypeArtifact, domain.MemoryTypeEntity, domain.MemoryTypeAgentAction} {
		item := domain.MemoryDocument{ID: uuid.NewString(), UserID: owner, Type: kind, Content: "initial content"}
		if err := repo.StoreDocument(ctx, item); err != nil {
			t.Fatal(kind, err)
		}
		item, err := repo.GetDocument(ctx, owner, item.ID, kind)
		if err != nil {
			t.Fatal(kind, err)
		}
		edited, err := s.EditDocument(ctx, owner, item.ID, kind, "edited "+string(kind), domain.MemoryWriteCondition{UpdatedAt: item.UpdatedAt, GenerationID: item.GenerationID})
		if err != nil || edited.Content != "edited "+string(kind) {
			t.Fatal(kind, edited, err)
		}
	}

	condition := domain.MemoryWriteCondition{UpdatedAt: doc.UpdatedAt, GenerationID: doc.GenerationID}
	// Exactly one simultaneous editor may commit a snapshot, including outbox/audit.
	var won atomic.Int32
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			_, err := s.EditDocument(ctx, owner, doc.ID, doc.Type, "after", condition)
			if err == nil {
				won.Add(1)
			} else if !errors.Is(err, domain.ErrAccessConflict) {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if won.Load() != 1 {
		t.Fatalf("document writers: %d", won.Load())
	}
	current, err := repo.GetDocument(ctx, owner, doc.ID, doc.Type)
	if err != nil || current.Content != "after" {
		t.Fatal(current, err)
	}
	if _, err = s.EditDocument(security.WithAccess(ctx, domain.AccessContext{UserID: "foreign", WorkspaceID: domain.PersonalWorkspaceID("foreign")}), owner, doc.ID, doc.Type, "leak", condition); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("scope", err)
	}
	if _, err = s.EditDocument(ctx, owner+"other", doc.ID, doc.Type, "leak", condition); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatal("owner", err)
	}
	oldCondition := domain.MemoryWriteCondition{UpdatedAt: current.UpdatedAt, GenerationID: current.GenerationID}
	if _, err = repo.UpdateDocument(ctx, owner, doc.ID, doc.Type, "legacy writer"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditDocument(ctx, owner, doc.ID, doc.Type, "overwrite legacy", oldCondition); !errors.Is(err, domain.ErrAccessConflict) {
		t.Fatal("legacy write did not invalidate snapshot", err)
	}
	current, err = repo.GetDocument(ctx, owner, doc.ID, doc.Type)
	if err != nil {
		t.Fatal(err)
	}
	wrongGeneration := domain.MemoryWriteCondition{UpdatedAt: current.UpdatedAt, GenerationID: "foreign-generation"}
	if _, err = s.EditDocument(ctx, owner, doc.ID, doc.Type, "wrong generation", wrongGeneration); err == nil {
		t.Fatal("generation fence bypassed")
	}
	// Create-only is atomic, and edits can explicitly clear a category.
	won.Store(0)
	for range 12 {
		group.Go(func() {
			_, err := s.EditKeyMemory(ctx, owner, "", "timezone", "Asia/Singapore", "profile", "pending", domain.MemoryWriteCondition{Create: true})
			if err == nil {
				won.Add(1)
			} else if !errors.Is(err, domain.ErrAccessConflict) {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if won.Load() != 1 {
		t.Fatalf("key creators: %d", won.Load())
	}
	km, ok, err := s.GetKeyMemory(ctx, owner, "timezone")
	if err != nil || !ok {
		t.Fatal(km, err)
	}
	keyCondition := domain.MemoryWriteCondition{UpdatedAt: km.UpdatedAt}
	won.Store(0)
	for range 12 {
		group.Go(func() {
			_, err := s.EditKeyMemory(ctx, owner, km.ID, "", "UTC", "", "approved", keyCondition)
			if err == nil {
				won.Add(1)
			} else if !errors.Is(err, domain.ErrAccessConflict) {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if won.Load() != 1 {
		t.Fatalf("key editors: %d", won.Load())
	}
	updated, ok, err := s.GetKeyMemory(ctx, owner, "timezone")
	if err != nil || !ok || updated.Category != "" || updated.Value != "UTC" {
		t.Fatal("category clear", updated, err)
	}
	if err = s.RemoveKeyMemory(ctx, owner, km.ID, keyCondition); !errors.Is(err, domain.ErrAccessConflict) {
		t.Fatal("stale delete", err)
	}
	// A legacy writer must invalidate the new API's precondition too.
	stale := domain.MemoryWriteCondition{UpdatedAt: updated.UpdatedAt}
	updated.Value = "legacy value"
	if _, err = s.UpsertKeyMemory(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveKeyMemory(ctx, owner, updated.ID, stale); !errors.Is(err, domain.ErrAccessConflict) {
		t.Fatal("legacy key update", err)
	}
	updated, _, err = s.GetKeyMemory(ctx, owner, "timezone")
	if err != nil {
		t.Fatal(err)
	}
	stale = domain.MemoryWriteCondition{UpdatedAt: updated.UpdatedAt}
	if err = s.RemoveKeyMemory(ctx, owner, updated.ID, stale); err != nil {
		t.Fatal(err)
	}
	recreated, err := s.EditKeyMemory(ctx, owner, "", "timezone", "recreated", "", "pending", domain.MemoryWriteCondition{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveKeyMemory(ctx, owner, updated.ID, stale); err == nil {
		t.Fatal("deleted a recreated key from stale page")
	}
	if db != nil {
		var audits, outbox int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='update_memory_document'`, doc.ID).Scan(&audits); err != nil || audits != 1 {
			t.Fatal("document audit count", audits, err)
		}
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type=$2`, doc.NodeID, domain.TaskTypeEmbeddingGenerate).Scan(&outbox); err != nil || outbox != 2 {
			t.Fatal("embedding outbox count", outbox, err)
		}
		// Inject a failure scoped to this generated test owner; no partial writes.
		name := "memory_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err = db.ExecContext(ctx, `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.payload->>'user_id'='`+owner+`' THEN RAISE EXCEPTION 'test audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER `+name+` BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION `+name+`()`) // only generated owner and identifier
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := db.ExecContext(ctx, `DROP TRIGGER `+name+` ON audit_events; DROP FUNCTION `+name+`() `); err != nil {
				t.Error(err)
			}
		}()
		if _, err = s.EditDocument(ctx, owner, doc.ID, doc.Type, "must roll back", domain.MemoryWriteCondition{UpdatedAt: current.UpdatedAt, GenerationID: current.GenerationID}); err == nil {
			t.Fatal("unaudited document write")
		}
		if _, err = s.EditKeyMemory(ctx, owner, recreated.ID, "", "must roll back", "", "pending", domain.MemoryWriteCondition{UpdatedAt: recreated.UpdatedAt}); err == nil {
			t.Fatal("unaudited key write")
		}
		if err = s.RemoveKeyMemory(ctx, owner, recreated.ID, domain.MemoryWriteCondition{UpdatedAt: recreated.UpdatedAt}); err == nil {
			t.Fatal("unaudited delete")
		}
		after, err := repo.GetDocument(ctx, owner, doc.ID, doc.Type)
		if err != nil || after.Content != current.Content || !after.UpdatedAt.Equal(current.UpdatedAt) {
			t.Fatal("partial document mutation", after, err)
		}
		afterKey, ok, err := s.GetKeyMemory(ctx, owner, "timezone")
		if err != nil || !ok || afterKey.Value != recreated.Value || !afterKey.UpdatedAt.Equal(recreated.UpdatedAt) {
			t.Fatal("partial key mutation", afterKey, err)
		}
		var afterOutbox int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type=$2`, doc.NodeID, domain.TaskTypeEmbeddingGenerate).Scan(&afterOutbox); err != nil || afterOutbox != outbox {
			t.Fatal("partial outbox mutation", afterOutbox, err)
		}
	}
}
