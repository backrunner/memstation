package memory

import (
	"context"
	"reflect"
	"testing"
	"time"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

func TestRelevancePolicyPreservesRelevantEvidenceAcrossSearchPaths(t *testing.T) {
	for _, path := range []string{"vector", "lexical", "historical"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
			repo := memorystore.NewInMemoryMemoryRepository()
			for i, id := range []string{"a", "b"} {
				doc := domain.MemoryDocument{ID: id, NodeID: id, UserID: "u", ConversationID: "conversation", Type: domain.MemoryTypeFact, Content: "owner " + id, Importance: 2 - float64(i)*1.5, DecayFactor: 1, ActivityAt: now, CreatedAt: now, UpdatedAt: now}
				if err := repo.StoreDocument(ctx, doc); err != nil {
					t.Fatal(err)
				}
			}
			e := NewEngine(repo, nil)
			e.SetClock(func() time.Time { return now })
			if path == "vector" {
				e.vectors = fixedRerankVectors{}
			}
			input := domain.SearchInput{UserID: "u", Query: "owner", TopN: 2, IncludeConversation: true}
			if path == "historical" {
				input.AsOf = &now
			}
			cfg := RerankConfig{Model: "test:model"}
			e.SetRerankConfig(cfg)
			e.SetReranker(rerankFunc(func(context.Context, domain.RerankInput) ([]domain.RerankResult, error) {
				return []domain.RerankResult{{DocumentID: "a", Score: 0}, {DocumentID: "b", Score: .99}}, nil
			}))
			got, err := e.SearchWithVariant(ctx, input, SearchVariantFull)
			if err != nil || got.Rerank.Status != "applied" || got.Result.Memories[0].ID != "b" || got.Components["a"].EffectiveScore != 0 || got.Components["b"].ScorePolicy != "relevance_v1" {
				t.Fatalf("lost relevant evidence or resurrected a zero score: %+v %v", got, err)
			}
			// An invalid batch must preserve the complete bounded hybrid result.
			e.SetRerankConfig(RerankConfig{})
			baseline, err := e.SearchWithVariant(ctx, input, SearchVariantFull)
			if err != nil {
				t.Fatal(err)
			}
			e.SetRerankConfig(cfg)
			e.SetReranker(rerankFunc(func(context.Context, domain.RerankInput) ([]domain.RerankResult, error) {
				return []domain.RerankResult{{DocumentID: "a", Score: 1}}, nil
			}))
			fallback, err := e.SearchWithVariant(ctx, input, SearchVariantFull)
			if err != nil || fallback.Rerank.Status != "degraded" || !reflect.DeepEqual(fallback.Result, baseline.Result) || !reflect.DeepEqual(fallback.Components, baseline.Components) {
				t.Fatalf("fallback changed first-stage results: %+v %v", fallback, err)
			}
		})
	}
}

func TestRelevancePolicyBoundsSecondarySignalsAndRetainsStalePenalty(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	signals := ScoreComponents{BaseSemanticScore: .8, KeywordScore: .95, RelationExpansion: 5, ConversationBoost: 5, SupersessionBoost: 5, RerankScore: .8}
	for _, importance := range []float64{.5, 1, 2} {
		doc := domain.MemoryDocument{Type: domain.MemoryTypeFact, Importance: importance, DecayFactor: 1, ActivityAt: now.AddDate(-10, 0, 0)}
		c := ExplainDocumentScoreAt(doc, signals, now, SearchVariantFull, true)
		if c.SemanticScore != .8 || !c.RerankApplied || c.ImportanceWeight < .9 || c.ImportanceWeight > 1.1 || c.TimeDecay < .9 || c.TimeDecay > 1 || c.EffectiveScore > .8*1.1 {
			t.Fatalf("secondary signals overwhelmed relevance: %+v", c)
		}
		doc.Metadata = map[string]any{"is_superseded": true}
		stale := ExplainDocumentScoreAt(doc, signals, now, SearchVariantFull, true)
		hybrid := ExplainDocumentScoreAt(doc, signals, now, SearchVariantFull, false)
		if stale.FreshnessPenalty >= 1 || stale.FreshnessPenalty != hybrid.FreshnessPenalty || stale.EffectiveScore >= c.EffectiveScore {
			t.Fatalf("stale protection weakened: fresh=%+v stale=%+v", c, stale)
		}
	}
}

func TestRerankingEnabledOnlyWithModel(t *testing.T) {
	cfg := RerankConfig{Model: "test:model"}
	if !cfg.enabled() {
		t.Fatal("configured model must enable reranking")
	}
	cfg.Model = ""
	if cfg.enabled() {
		t.Fatal("empty model must disable reranking ")
	}
}

type policyScopeVectors struct{ ports.VectorStore }

func (policyScopeVectors) Search(context.Context, domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	var out []domain.VectorSearchResult
	for _, id := range []string{"a", "b", "foreign-user", "foreign-workspace", "foreign-partition", "foreign-generation"} {
		out = append(out, domain.VectorSearchResult{NodeID: id, Score: .99})
	}
	return out, nil
}

func TestRelevancePolicyScopesCandidatesBeforeInference(t *testing.T) {
	for _, path := range []string{"vector", "lexical", "historical"} {
		t.Run(path, func(t *testing.T) {
			repo := memorystore.NewInMemoryMemoryRepository()
			now := time.Now().UTC()
			for _, item := range []struct{ id, user, workspace, partition, generation string }{
				{"a", "owner", "workspace", "private", "g1"},
				{"b", "owner", "workspace", "private", "g1"},
				{"foreign-user", "other", "workspace", "private", "g1"},
				{"foreign-workspace", "owner", "other-workspace", "private", "g1"},
				{"foreign-partition", "owner", "workspace", "other-partition", "g1"},
				{"foreign-generation", "owner", "workspace", "private", "g2"},
			} {
				ctx, err := security.WithDataScope(context.Background(), item.user, item.workspace, item.partition)
				if err != nil {
					t.Fatal(err)
				}
				doc := domain.MemoryDocument{ID: item.id, NodeID: item.id, UserID: item.user, WorkspaceID: item.workspace, PartitionID: item.partition, GenerationID: item.generation, Type: domain.MemoryTypeFact, Content: "owner position", Importance: 2, ActivityAt: now, CreatedAt: now, UpdatedAt: now}
				if err := repo.StoreDocument(ctx, doc); err != nil {
					t.Fatal(err)
				}
			}
			e := NewEngine(repo, nil)
			if path == "vector" {
				e.vectors = policyScopeVectors{}
			}
			e.SetRerankConfig(RerankConfig{Model: "test:model"})
			called := false
			e.SetReranker(rerankFunc(func(_ context.Context, input domain.RerankInput) ([]domain.RerankResult, error) {
				called = true
				if len(input.Documents) != 2 {
					t.Fatalf("foreign candidate reached inference: %+v", input.Documents)
				}
				for _, d := range input.Documents {
					if d.ID != "a" && d.ID != "b" {
						t.Fatalf("foreign candidate reached inference: %s", d.ID)
					}
				}
				return []domain.RerankResult{{DocumentID: "a", Score: .9}, {DocumentID: "b", Score: .8}}, nil
			}))
			ctx, err := security.WithDataScope(context.Background(), "owner", "workspace", "private")
			if err != nil {
				t.Fatal(err)
			}
			input := domain.SearchInput{UserID: "owner", WorkspaceID: "workspace", GenerationID: "g1", Query: "owner", TopN: 2, IncludeConversation: true}
			if path == "historical" {
				input.AsOf = &now
			}
			got, err := e.SearchWithVariant(ctx, input, SearchVariantFull)
			if err != nil || !called || len(got.Result.Memories) != 2 {
				t.Fatalf("scoped relevance query failed: %+v %v", got, err)
			}
		})
	}
}
