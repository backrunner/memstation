package sql_test

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
	"memstation/apps/server/internal/service/admin"
	"memstation/apps/server/internal/service/browse"
	"memstation/apps/server/internal/service/memory"
	sqlstore "memstation/apps/server/internal/store/sql"

	"github.com/google/uuid"
)

func TestRecordBrowse(t *testing.T) {
	runRecordBrowse(t, sqlstore.NewInMemoryConversationRepository(), sqlstore.NewInMemoryMemoryRepository(), nil)
}

func TestPostgresRecordBrowse(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runRecordBrowse(t, sqlstore.NewPostgresConversationRepository(db), sqlstore.NewPostgresMemoryRepository(db), db)
}

func runRecordBrowse(t *testing.T, conversations ports.ConversationRepository, memories ports.MemoryRepository, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	owner := "record-browse-" + uuid.NewString()
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC)
	wanted := map[string]bool{}
	for i := range 109 {
		suffix := fmt.Sprintf("%03d", i)
		if i == 0 {
			suffix = "中文"
		}
		if i == 1 {
			suffix = "Z"
		}
		id := owner + "-" + suffix
		value := "recent unrelated"
		when := stamp.Add(time.Hour)
		if i < 4 {
			value = "old Needle_%"
			when = stamp
			wanted[id] = true
		}
		provider := domain.ProviderOpenAI
		if i < 4 {
			provider = domain.Provider("Needle_%")
		}
		if err := conversations.PersistExchange(ctx, domain.ConversationRecord{UserID: owner, ConversationID: id, Provider: provider, CreatedAt: when, UpdatedAt: when, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "message"}}, Usage: domain.UsageRecord{TotalTokens: 17}}); err != nil {
			t.Fatal(err)
		}
		if err := memories.UpsertKeyMemory(ctx, domain.KeyMemory{ID: id, UserID: owner, Key: suffix, Value: value}); err != nil {
			t.Fatal(err)
		}
		// Both stores stamp key writes at commit time. Fix timestamps in the test
		// fixture to exercise tied timestamp ordering and old-record filtering.
		if db != nil {
			if _, err := db.ExecContext(ctx, "UPDATE key_memories SET updated_at=$1 WHERE user_id=$2 AND id=$3", when, owner, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{UserID: owner + "-foreign", ConversationID: uuid.NewString(), Provider: domain.Provider("Needle_%")}); err != nil {
		t.Fatal(err)
	}
	if err := memories.UpsertKeyMemory(ctx, domain.KeyMemory{UserID: owner + "-foreign", Key: "foreign", Value: "Needle_%"}); err != nil {
		t.Fatal(err)
	}
	adminService := admin.NewService(conversations, nil, memories, nil, nil, nil)
	memoryService := memory.NewService(nil, memories)
	cursor := checkRecordPages(t, owner, wanted, adminService.BrowseConversations, func(item domain.ConversationListItem) string {
		if item.UserID != owner || item.MessageCount != 1 || item.TotalTokens != 17 {
			t.Fatalf("wrong scope or aggregates: %+v", item)
		}
		return item.ConversationID
	})
	checkRecordPages(t, owner, wanted, memoryService.BrowseKeyMemories, func(item domain.KeyMemory) string {
		if item.UserID != owner {
			t.Fatal("wrong owner")
		}
		return item.ID
	})
	if _, err := memoryService.BrowseKeyMemories(ctx, domain.RecordBrowseQuery{UserID: owner, Query: "needle_%", After: cursor, Limit: 2}); !errors.Is(err, browse.ErrInvalid) {
		t.Fatal("cross-collection cursor accepted", err)
	}
	if err := memories.DeleteKeyMemory(ctx, owner, "中文"); err != nil {
		t.Fatal(err)
	}
	page, err := memoryService.BrowseKeyMemories(ctx, domain.RecordBrowseQuery{UserID: owner, Query: "needle_%", Limit: 100})
	if err != nil || len(page.Items) != 3 {
		t.Fatal("deleted key returned", page, err)
	}
}

func checkRecordPages[T any](t *testing.T, owner string, wanted map[string]bool, fetch func(context.Context, domain.RecordBrowseQuery) (domain.RecordPage[T], error), id func(T) string) string {
	t.Helper()
	ctx := context.Background()
	input := domain.RecordBrowseQuery{UserID: owner, Query: "needle_%", Limit: 2}
	seen := map[string]bool{}
	firstCursor := ""
	for range 10 {
		page, err := fetch(ctx, input)
		if err != nil || len(page.Items) > 2 {
			t.Fatal("unbounded page", err)
		}
		for _, item := range page.Items {
			key := id(item)
			if !wanted[key] || seen[key] {
				t.Fatal("foreign or duplicated row", key)
			}
			seen[key] = true
		}
		if firstCursor == "" {
			firstCursor = page.NextCursor
		}
		if page.NextCursor == "" {
			break
		}
		input.After = page.NextCursor
	}
	if len(seen) != len(wanted) || firstCursor == "" {
		t.Fatal("missing records", seen)
	}
	for _, change := range []func(*domain.RecordBrowseQuery){
		func(q *domain.RecordBrowseQuery) { q.UserID += "-foreign" },
		func(q *domain.RecordBrowseQuery) { q.Query = "other" },
		func(q *domain.RecordBrowseQuery) { q.After = "broken" },
		func(q *domain.RecordBrowseQuery) { q.Limit = 0 },
		func(q *domain.RecordBrowseQuery) { q.Limit = 101 },
	} {
		bad := input
		bad.After = firstCursor
		change(&bad)
		if _, err := fetch(ctx, bad); !errors.Is(err, browse.ErrInvalid) {
			t.Fatal("invalid cursor accepted", err)
		}
	}
	if _, err := fetch(security.WithAccess(ctx, domain.AccessContext{UserID: owner, WorkspaceID: "group"}), input); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("unsupported scope accepted", err)
	}
	input.After = ""
	input.Query = "%_"
	page, err := fetch(ctx, input)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("wildcard interpreted", err)
	}
	return firstCursor
}
