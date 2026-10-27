package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/cache"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

func TestKeyMemoryCacheValidatesScopeBeforeHit(t *testing.T) {
	ctx := context.Background()
	store, err := cache.NewJSONStore(ctx, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(ctx) })
	repo := memorystore.NewInMemoryMemoryRepository()
	service := NewService(nil, repo)
	service.SetKeyMemoryCache(store)
	if err := repo.UpsertKeyMemory(ctx, domain.KeyMemory{UserID: "owner", Key: "focus", Value: "visible"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListKeyMemories(ctx, "owner", 10); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []domain.AccessContext{
		{UserID: "other", WorkspaceID: domain.PersonalWorkspaceID("other")},
		{UserID: "owner", WorkspaceID: "group"},
	} {
		if _, err := service.ListKeyMemories(security.WithAccess(ctx, scope), "owner", 10); !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatalf("cached read accepted %+v: %v", scope, err)
		}
	}
	for _, invalid := range []domain.KeyMemory{
		{UserID: "other", WorkspaceID: domain.PersonalWorkspaceID("owner"), Value: "foreign owner"},
		{UserID: "owner", WorkspaceID: "group", Value: "foreign workspace"},
		{UserID: "owner", Value: "legacy cache missing scope"},
	} {
		if err := store.Set(ctx, keyMemoryCacheKey(ctx, "owner", 10), []domain.KeyMemory{invalid}); err != nil {
			t.Fatal(err)
		}
		got, err := service.ListKeyMemories(ctx, "owner", 10)
		if err != nil || len(got) != 1 || got[0].Value != "visible" {
			t.Fatalf("cache scope validation: %+v %v", got, err)
		}
	}
	neighbor := keyMemoryCacheKey(ctx, "owner:limit:10", 10)
	if err := store.Set(ctx, neighbor, []string{"preserve"}); err != nil {
		t.Fatal(err)
	}
	service.InvalidateKeyMemoryCache(ctx, "owner")
	var got []string
	if ok, err := store.Get(ctx, neighbor, &got); err != nil || !ok || len(got) != 1 {
		t.Fatalf("invalidation crossed owner boundary: %v %v", ok, err)
	}
}

type deniedScopeVectors struct {
	ports.VectorStore
	calls int
	t     *testing.T
}

func (v *deniedScopeVectors) Search(ctx context.Context, input domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	v.calls++
	if input.WorkspaceID != domain.PersonalWorkspaceID(input.UserID) {
		v.t.Errorf("missing typed vector scope: %+v", input)
	}
	if _, err := security.PersonalScope(ctx, "other", ""); !errors.Is(err, domain.ErrAccessDenied) {
		v.t.Error("vector call lost bound context")
	}
	return nil, domain.ErrAccessDenied
}

func TestEngineDoesNotFallbackAfterScopeDenial(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: "fact", UserID: "owner", Type: domain.MemoryTypeFact, Content: "visible fallback fact"}); err != nil {
		t.Fatal(err)
	}
	vectors := &deniedScopeVectors{t: t}
	engine := NewEngine(repo, vectors)
	at := time.Now().UTC().Add(time.Minute)
	for _, asOf := range []*time.Time{nil, &at} {
		if _, err := engine.Search(ctx, domain.SearchInput{UserID: "owner", Query: "fact", AsOf: asOf}); !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatalf("scope denial fell back (historical=%v): %v", asOf != nil, err)
		}
	}
	if vectors.calls != 2 {
		t.Fatalf("expected both vector paths, got %d", vectors.calls)
	}
	if _, err := engine.Search(ctx, domain.SearchInput{UserID: "owner", WorkspaceID: "group", Query: "fact"}); !errors.Is(err, domain.ErrAccessDenied) || vectors.calls != 2 {
		t.Fatal("unsupported scope reached vector provider", err)
	}
}
