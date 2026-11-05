package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	memorystore "memstation/apps/server/internal/store/sql/memory"
)

type queryExtractorFunc func(context.Context, string, string) (domain.QueryEntities, error)

func (f queryExtractorFunc) ExtractQueryEntities(ctx context.Context, u, q string) (domain.QueryEntities, error) {
	return f(ctx, u, q)
}

// Isolate candidate admission: this fixture has no vector/keyword candidates.
type entityAdmissionRepo struct{ ports.MemoryRepository }

func (r entityAdmissionRepo) SearchKeywordMatches(context.Context, string, string, string, int, ...domain.MemoryType) ([]domain.KeywordMatch, error) {
	return nil, nil
}

func (r entityAdmissionRepo) Search(context.Context, domain.SearchInput) (domain.SearchResult, error) {
	return domain.SearchResult{}, nil
}

func TestEntityRecallAddsSourceBeyondDirectPoolAndHasHonestAblation(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	for _, d := range []domain.MemoryDocument{
		{UserID: "owner", ID: "mention", NodeID: "mention", Type: domain.MemoryTypeEntity, Content: "MemStation", Metadata: map[string]any{"canonical_name": "MemStation", "interpretation": "source_mention"}},
		{UserID: "owner", ID: "source", NodeID: "source", Type: domain.MemoryTypeFact, Content: "The migration completed on Monday.", Metadata: map[string]any{"fact_key": "migration"}},
	} {
		if err := repo.StoreDocument(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{UserID: "owner", SrcNodeID: "source", DstNodeID: "mention", RelationType: domain.MemoryRelationMentionsEntity, Weight: 1}); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(entityAdmissionRepo{repo}, nil)
	calls := 0
	engine.SetEntityRecall(queryExtractorFunc(func(context.Context, string, string) (domain.QueryEntities, error) {
		calls++
		return domain.QueryEntities{Names: []string{"memstation"}}, nil
	}), time.Second)
	input := domain.SearchInput{UserID: "owner", Query: "MemStation 最近的迁移结果是什么？", MemoryTypes: []domain.MemoryType{domain.MemoryTypeFact}, TopN: 5, MinScore: .3}
	result, err := engine.SearchWithVariant(ctx, input, SearchVariantFull)
	if err != nil || len(result.Result.Memories) != 1 || result.Result.Memories[0].ID != "source" {
		t.Fatalf("entity posting did not enter pool: %+v %v", result, err)
	}
	if result.Rerank.ScorePolicy != result.Components["source"].ScorePolicy {
		t.Fatal("rerank diagnostics disagree with applied scoring policy", result.Rerank, result.Components)
	}
	if result.Components["source"].EntityScore <= 0 || result.Components["source"].KeywordScore != 0 || result.Components["source"].BaseSemanticScore != 0 {
		t.Fatalf("entity signal mislabeled: %+v", result.Components)
	}
	if result.Result.EntityRecall.Status != "complete" || result.Result.EntityRecall.MatchedNodes != 2 {
		t.Fatal(result.Result.EntityRecall)
	}
	baseline, err := engine.SearchWithVariant(ctx, input, SearchVariantFullNoEntities)
	if err != nil || len(baseline.Result.Memories) != 0 || calls != 1 || baseline.Result.EntityRecall.Status != "ablation" {
		t.Fatalf("ablation called model or retained entity signal: %+v %v", baseline, err)
	}
	if err := repo.StoreDocument(ctx, domain.MemoryDocument{UserID: "owner", ID: "current", NodeID: "current", Type: domain.MemoryTypeFact, Content: "The migration was corrected on Tuesday.", Metadata: map[string]any{"fact_key": "migration"}}); err != nil {
		t.Fatal(err)
	}
	current, err := engine.SearchWithVariant(ctx, input, SearchVariantFull)
	if err != nil || len(current.Result.Memories) != 1 || current.Result.Memories[0].ID != "current" || current.Components["current"].EntityScore <= 0 {
		t.Fatalf("entity signal lost or stale head returned: %+v %v", current, err)
	}
	if err := repo.DeleteDocument(ctx, "owner", "source", domain.MemoryTypeFact); err != nil {
		t.Fatal(err)
	}
	result, err = engine.SearchWithVariant(ctx, input, SearchVariantFull)
	if err != nil || len(result.Result.Memories) != 0 {
		t.Fatal("erased source returned through entity posting", result, err)
	}
}

func TestEntityRecallOutageIsExplicitAndAuthorityErrorsFailClosed(t *testing.T) {
	for _, mode := range []string{"outage", "timeout", "access"} {
		t.Run(mode, func(t *testing.T) {
			engine := NewEngine(memorystore.NewInMemoryMemoryRepository(), nil)
			engine.SetEntityRecall(queryExtractorFunc(func(ctx context.Context, _ string, _ string) (domain.QueryEntities, error) {
				if mode == "access" {
					return domain.QueryEntities{}, domain.ErrAccessDenied
				}
				if mode == "timeout" {
					<-ctx.Done()
					return domain.QueryEntities{}, ctx.Err()
				}
				return domain.QueryEntities{}, errors.New("encoder unavailable")
			}), 10*time.Millisecond)
			result, err := engine.Search(context.Background(), domain.SearchInput{UserID: "owner", Query: "project"})
			if mode == "access" {
				if !errors.Is(err, domain.ErrAccessDenied) {
					t.Fatal("authority failure degraded", err)
				}
				return
			}
			want := "unavailable"
			if mode == "timeout" {
				want = "timed_out"
			}
			if err != nil || result.EntityRecall == nil || result.EntityRecall.Status != want {
				t.Fatalf("silent degradation: %+v %v", result, err)
			}
		})
	}
}

func TestEntityNameIndexKeepsNamesSeparateAndRespectsAudience(t *testing.T) {
	ctx, err := security.WithDataScope(context.Background(), "owner", "workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	privateCtx, err := security.WithDataScope(context.Background(), "owner", "workspace", "private")
	if err != nil {
		t.Fatal(err)
	}
	repo := memorystore.NewInMemoryMemoryRepository()
	for _, item := range []struct {
		ctx         context.Context
		id, g, name string
	}{{ctx, "a", "generation", "小林"}, {ctx, "b", "generation", "小林"}, {privateCtx, "private", "generation", "小林"}, {ctx, "old", "old-generation", "小林"}, {ctx, "other-name", "generation", "小琳"}} {
		if err := repo.StoreDocument(item.ctx, domain.MemoryDocument{UserID: "owner", GenerationID: item.g, ID: item.id, NodeID: item.id, Type: domain.MemoryTypeEntity, Content: item.name, Metadata: map[string]any{"canonical_name": item.name}}); err != nil {
			t.Fatal(err)
		}
	}
	matches, err := repo.MatchEntityNames(ctx, "owner", "generation", "", []string{"小林"})
	if err != nil || len(matches) != 2 || matches[0].Occurrences != 2 || matches[0].NodeID == matches[1].NodeID {
		t.Fatalf("identity collapsed or scope leaked: %+v %v", matches, err)
	}
}

func TestEntityChannelDeadlineDoesNotWaitForUncooperativeAdapter(t *testing.T) {
	gate := make(chan struct{})
	finished := make(chan struct{})
	defer close(gate)
	engine := NewEngine(memorystore.NewInMemoryMemoryRepository(), nil)
	engine.SetEntityRecall(queryExtractorFunc(func(context.Context, string, string) (domain.QueryEntities, error) {
		defer close(finished)
		<-gate
		return domain.QueryEntities{}, nil
	}), 10*time.Millisecond)
	returned := make(chan domain.SearchResult, 1)
	go func() {
		result, _ := engine.Search(context.Background(), domain.SearchInput{UserID: "owner", Query: "project"})
		returned <- result
	}()
	select {
	case result := <-returned:
		if result.EntityRecall == nil || result.EntityRecall.Status != "timed_out" {
			t.Fatalf("deadline not surfaced: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("entity adapter blocked the whole search")
	}
}
