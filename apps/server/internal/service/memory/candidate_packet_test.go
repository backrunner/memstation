package memory

import (
	"context"
	"fmt"
	"testing"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
)

func TestEvidenceInjectionUsesSpareContextWithoutEnlargingSearchPage(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	for i := range 30 {
		doc := coreFixture(fmt.Sprintf("event-%02d", i))[0]
		doc.Content = fmt.Sprintf("signal source observation %02d", i)
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	engine := NewEngine(repo, nil)
	page, err := engine.Search(ctx, domain.SearchInput{UserID: "owner", Query: "signal", TopN: 8})
	if err != nil || len(page.Memories) != 8 {
		t.Fatal("public page limit changed", page.Total, err)
	}
	if err := engine.SetEvidenceConfig(EvidenceConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	req := domain.UnifiedRequest{UserID: "owner", Mode: domain.ModeInject, MaxInjectionTokens: 4096, InjectContext: true, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "signal"}}}
	injected, result, err := engine.Inject(ctx, req)
	if err != nil || len(result.Search.Memories) <= 8 || len(result.Search.Memories) > 50 || len(injected.Messages) != 2 || len(injected.Messages[0].Text) > 16384 {
		t.Fatal("context was paginated or exceeded budget", len(result.Search.Memories), err)
	}
	have := map[string]bool{}
	for _, doc := range result.Search.Memories {
		have[doc.ID] = true
	}
	for _, doc := range page.Memories {
		if !have[doc.ID] {
			t.Fatal("original result evicted by evidence-only candidate", doc.ID)
		}
	}
}
