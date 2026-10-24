package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	memoryservice "memstation/apps/server/internal/service/memory"

	"github.com/google/uuid"
)

func TestMemoryBrowse(t *testing.T) { runMemoryBrowse(t, NewInMemoryMemoryRepository()) }
func TestPostgresMemoryBrowse(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runMemoryBrowse(t, NewPostgresMemoryRepository(db))
}

func runMemoryBrowse(t *testing.T, repo ports.MemoryRepository) {
	t.Helper()
	ctx := context.Background()
	owner := "memory-browse-" + uuid.NewString()
	service := memoryservice.NewService(nil, repo)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC)
	kinds := []domain.MemoryType{domain.MemoryTypeSummary, domain.MemoryTypeFact, domain.MemoryTypeImpression, domain.MemoryTypeEvent, domain.MemoryTypeTask, domain.MemoryTypeProcedure, domain.MemoryTypeArtifact, domain.MemoryTypeEntity, domain.MemoryTypeAgentAction}
	wanted := map[string]bool{}
	for _, kind := range kinds {
		for _, suffix := range []string{"a", "Z", "中文"} {
			id := owner + "-" + string(kind) + "-" + suffix
			wanted[id] = true
			if err := repo.StoreDocument(ctx, domain.MemoryDocument{UserID: owner, ID: id, Type: kind, Content: "old Needle_% literal", UpdatedAt: stamp, CreatedAt: stamp}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// More than the old recent-100 window: filtering must precede the limit.
	for i := range 105 {
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{UserID: owner, ID: fmt.Sprintf("%s-new-%03d", owner, i), Type: domain.MemoryTypeSummary, Content: "recent unrelated", UpdatedAt: stamp.Add(time.Hour), CreatedAt: stamp}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.StoreDocument(ctx, domain.MemoryDocument{UserID: owner + "-foreign", ID: uuid.NewString(), Type: domain.MemoryTypeFact, Content: "Needle_% private"}); err != nil {
		t.Fatal(err)
	}
	input := domain.MemoryBrowseQuery{UserID: owner, Kinds: kinds, Query: "needle_%", Limit: 4}
	seen := map[string]bool{}
	var first domain.MemoryDocumentPage
	for pageNumber := 0; pageNumber < 20; pageNumber++ {
		page, err := service.BrowseDocuments(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if pageNumber == 0 {
			first = page
		}
		if len(page.Items) > input.Limit || page.GenerationID == "" {
			t.Fatal("unbounded or unpinned page", page)
		}
		for _, item := range page.Items {
			if !wanted[item.ID] || seen[item.ID] || item.UserID != owner {
				t.Fatal("unexpected or repeated record", item.ID)
			}
			seen[item.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		input.After = page.NextCursor
	}
	if len(seen) != len(wanted) || first.NextCursor == "" {
		t.Fatalf("pagination lost rows: %d / %d", len(seen), len(wanted))
	}
	for _, mutate := range []func(*domain.MemoryBrowseQuery){
		func(q *domain.MemoryBrowseQuery) { q.UserID += "-foreign" },
		func(q *domain.MemoryBrowseQuery) { q.Query = "other" },
		func(q *domain.MemoryBrowseQuery) { q.Kinds = []domain.MemoryType{domain.MemoryTypeSummary} },
		func(q *domain.MemoryBrowseQuery) { q.GenerationID = "different" },
		func(q *domain.MemoryBrowseQuery) { q.After = "invalid" },
	} {
		bad := input
		bad.After = first.NextCursor
		mutate(&bad)
		if _, err := service.BrowseDocuments(ctx, bad); !errors.Is(err, memoryservice.ErrInvalidBrowse) {
			t.Fatal("accepted mismatched cursor", err)
		}
	}
	if _, err := service.BrowseDocuments(security.WithAccess(ctx, domain.AccessContext{UserID: owner, WorkspaceID: "group"}), input); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("scope bypass", err)
	}
	input.After = ""
	input.Query = "_new_%" // SQL wildcard characters must be literal
	page, err := service.BrowseDocuments(ctx, input)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("wildcard interpreted", err)
	}
	input.Query = ""
	input.GenerationID = "missing"
	page, err = service.BrowseDocuments(ctx, input)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("generation leaked", err)
	}
	if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{UserID: owner, Key: "deleted", Value: "hidden"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteKeyMemory(ctx, owner, "deleted"); err != nil {
		t.Fatal(err)
	}
	input.GenerationID = ""
	input.Kinds = []domain.MemoryType{domain.MemoryTypeKeyMemory}
	page, err = service.BrowseDocuments(ctx, input)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("deleted key resurfaced", err)
	}
}
