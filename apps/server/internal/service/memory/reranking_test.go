package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

type rerankFunc func(context.Context, domain.RerankInput) ([]domain.RerankResult, error)

func (f rerankFunc) Rerank(ctx context.Context, input domain.RerankInput) ([]domain.RerankResult, error) {
	return f(ctx, input)
}

func TestRerankValidatesBatchAtomically(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []domain.RerankResult
		err     error
		reason  string
	}{
		{"unknown document", []domain.RerankResult{{DocumentID: "a", Score: .8}, {DocumentID: "other-user", Score: 1}}, nil, "invalid_response"},
		{"duplicate", []domain.RerankResult{{DocumentID: "a", Score: .8}, {DocumentID: "a", Score: .9}}, nil, "invalid_response"},
		{"nan", []domain.RerankResult{{DocumentID: "a", Score: .8}, {DocumentID: "b", Score: math.NaN()}}, nil, "invalid_response"},
		{"infinity", []domain.RerankResult{{DocumentID: "a", Score: .8}, {DocumentID: "b", Score: math.Inf(1)}}, nil, "invalid_response"},
		{"negative", []domain.RerankResult{{DocumentID: "a", Score: .8}, {DocumentID: "b", Score: -.1}}, nil, "invalid_response"},
		{"unbounded", []domain.RerankResult{{DocumentID: "a", Score: .8}, {DocumentID: "b", Score: 1.1}}, nil, "invalid_response"},
		{"partial", []domain.RerankResult{{DocumentID: "a", Score: .8}}, nil, "incomplete_response"},
		{"empty", nil, nil, "incomplete_response"},
		{"provider failure", nil, errors.New("private upstream prompt or credential"), "provider_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEngine(nil, nil)
			e.SetRerankConfig(RerankConfig{Model: "test:model"})
			e.SetReranker(rerankFunc(func(context.Context, domain.RerankInput) ([]domain.RerankResult, error) { return tc.results, tc.err }))
			scores, ranks, d := e.rerankCandidates(context.Background(), domain.SearchInput{Query: "owner?", TopN: 2}, []searchCandidate{
				{Document: domain.MemoryDocument{NodeID: "a", Content: "first"}},
				{Document: domain.MemoryDocument{NodeID: "b", Content: "second"}},
			})
			if len(scores) != 0 || len(ranks) != 0 || d.Status != "degraded" || d.Reason != tc.reason || d.Candidates != 2 || d.Scored != 0 {
				t.Fatalf("partially applied invalid rerank: scores=%v ranks=%v diagnostics=%+v", scores, ranks, d)
			}
			raw, err := json.Marshal(d)
			if err != nil || strings.Contains(string(raw), "private") {
				t.Fatalf("unsafe diagnostics: %s, %v", raw, err)
			}
		})
	}
}

func TestRerankBudgetAndZeroScore(t *testing.T) {
	e := NewEngine(nil, nil)
	e.SetRerankConfig(RerankConfig{Model: "test:model", MinCandidates: 2, MaxCandidates: 2})
	e.SetReranker(rerankFunc(func(_ context.Context, in domain.RerankInput) ([]domain.RerankResult, error) {
		if len(in.Documents) != 2 || in.TopN != 2 || in.Documents[0].ID != "a" || in.Documents[1].ID != "b" {
			t.Fatalf("unstable or unbounded candidates: %+v", in)
		}
		return []domain.RerankResult{{DocumentID: "a", Score: 0}, {DocumentID: "b", Score: 1}}, nil
	}))
	scores, ranks, d := e.rerankCandidates(context.Background(), domain.SearchInput{Query: "owner?", TopN: 2}, []searchCandidate{
		{Document: domain.MemoryDocument{NodeID: "c", Content: "third"}},
		{Document: domain.MemoryDocument{NodeID: "b", Content: "second"}},
		{Document: domain.MemoryDocument{NodeID: "a", Content: "first"}},
		{Document: domain.MemoryDocument{NodeID: "a", Content: "duplicate"}},
	})
	if d.Status != "applied" || d.Scored != 2 || ranks["b"] != 1 || ranks["a"] != 2 || scores["a"] != 0 {
		t.Fatalf("valid zero relevance was lost: scores=%v ranks=%v diagnostics=%+v", scores, ranks, d)
	}
}

func TestRerankDisabledUnavailableAndSkippedAreDistinct(t *testing.T) {
	e := NewEngine(nil, nil)
	_, _, d := e.rerankCandidates(context.Background(), domain.SearchInput{}, nil)
	if d.Status != "disabled" {
		t.Fatalf("expected disabled: %+v", d)
	}
	e.SetRerankConfig(RerankConfig{Model: "test:model"})
	_, _, d = e.rerankCandidates(context.Background(), domain.SearchInput{}, nil)
	if d.Status != "degraded" || d.Reason != "unavailable" {
		t.Fatalf("expected unavailable: %+v", d)
	}
	e.SetReranker(rerankFunc(func(context.Context, domain.RerankInput) ([]domain.RerankResult, error) {
		t.Fatal("skipped query reached provider")
		return nil, nil
	}))
	_, _, d = e.rerankCandidates(context.Background(), domain.SearchInput{Query: "owner?"}, nil)
	if d.Status != "skipped" || d.Reason != "insufficient_candidates" {
		t.Fatalf("expected skipped: %+v", d)
	}
}

type fixedRerankVectors struct{ ports.VectorStore }

func (fixedRerankVectors) Search(context.Context, domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	return []domain.VectorSearchResult{{NodeID: "a", Score: .9}, {NodeID: "b", Score: .7}}, nil
}

func TestSuccessfulRerankExcludesUnscoredPool(t *testing.T) {
	for _, path := range []string{"vector", "lexical", "historical"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			repo := memorystore.NewInMemoryMemoryRepository()
			now := time.Now().UTC()
			for i, id := range []string{"a", "b", "c"} {
				if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: id, NodeID: id, UserID: "u", ConversationID: "conv", Type: domain.MemoryTypeFact, Content: "owner " + id, Importance: 1 - float64(i)*.05, DecayFactor: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
			}
			e := NewEngine(repo, nil)
			e.SetClock(func() time.Time { return now })
			input := domain.SearchInput{UserID: "u", Query: "owner", TopN: 1, IncludeConversation: true}
			if path == "historical" {
				input.AsOf = &now
			}
			// Give all scored documents zero relevance: an unscored document
			// must not win merely by retaining its first-stage score.
			submitted := map[string]bool{}
			e.SetReranker(rerankFunc(func(_ context.Context, in domain.RerankInput) ([]domain.RerankResult, error) {
				out := make([]domain.RerankResult, 0, len(in.Documents))
				for _, d := range in.Documents {
					submitted[d.ID] = true
					out = append(out, domain.RerankResult{DocumentID: d.ID, Score: 0})
				}
				return out, nil
			}))
			// Repository candidate expansion is allowed to exceed the rerank cap.
			e.SetRerankConfig(RerankConfig{Model: "test:model", MinCandidates: 2, MaxCandidates: 2})
			if path == "vector" {
				e.vectors = unboundedRerankVectors{}
			}
			got, err := e.SearchWithVariant(ctx, input, SearchVariantFull)
			if err != nil || got.Rerank.Status != "applied" || got.Rerank.Excluded != 1 || len(got.Result.Memories) != 1 {
				t.Fatalf("pool: %+v %v", got, err)
			}
			for _, d := range got.Result.Memories {
				if !submitted[d.NodeID] {
					t.Fatal("unscored document bypassed reranker")
				}
			}
		})
	}
}

type unboundedRerankVectors struct{ ports.VectorStore }

func (unboundedRerankVectors) Search(context.Context, domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	return []domain.VectorSearchResult{{NodeID: "a", Score: .9}, {NodeID: "b", Score: .85}, {NodeID: "c", Score: .8}}, nil
}

func TestCandidateCollectionIsIndependentOfRerankToggle(t *testing.T) {
	e := NewEngine(nil, nil)
	for _, model := range []string{"", "test:model"} {
		e.SetRerankConfig(RerankConfig{Model: model, MinCandidates: 40, MaxCandidates: 50, CandidateMultiplier: 6})
		if got := e.candidateRecallLimit(1); got != 40 {
			t.Fatalf("small top_n narrowed recall: %d", got)
		}
		if got := e.candidateRecallLimit(10); got != 50 {
			t.Fatalf("candidate cap ignored: %d", got)
		}
		if got := e.candidateRecallLimit(100); got != 200 {
			t.Fatalf("final top_n underfilled: %d", got)
		}
	}
}

func TestRerankBudgetDoesNotSilentlyUnderfillRequestedPage(t *testing.T) {
	e := NewEngine(nil, nil)
	e.SetRerankConfig(RerankConfig{Model: "test:model", MinCandidates: 2, MaxCandidates: 2})
	e.SetReranker(rerankFunc(func(context.Context, domain.RerankInput) ([]domain.RerankResult, error) {
		t.Fatal("request exceeds configured inference budget")
		return nil, nil
	}))
	_, ranks, d := e.rerankCandidates(context.Background(), domain.SearchInput{Query: "owner", TopN: 3}, nil)
	if d.Status != "skipped" || d.Reason != "candidate_budget_below_top_n" || !rerankEligible("unscored", ranks, d) {
		t.Fatalf("must retain first-stage results without exceeding inference budget: %+v", d)
	}
}

func TestRerankChannelReservationsPreserveComplementaryEvidence(t *testing.T) {
	// Three broad lexical hits outrank two strong dense-only candidates under
	// max-score fusion. A three-document pool must retain both signal sources.
	ordered := []searchCandidate{
		{Document: domain.MemoryDocument{NodeID: "lexical-a", Content: "a"}, KeywordScore: .95, PreliminaryScore: .95},
		{Document: domain.MemoryDocument{NodeID: "lexical-b", Content: "b"}, KeywordScore: .94, PreliminaryScore: .94},
		{Document: domain.MemoryDocument{NodeID: "lexical-c", Content: "c"}, KeywordScore: .93, PreliminaryScore: .93},
		{Document: domain.MemoryDocument{NodeID: "dense-a", Content: "d"}, BaseSemanticScore: .8, PreliminaryScore: .8},
		{Document: domain.MemoryDocument{NodeID: "dense-b", Content: "e"}, BaseSemanticScore: .7, PreliminaryScore: .7},
	}
	got := balanceRerankChannels(ordered, 3)
	if got[0].Document.NodeID != "dense-a" || got[1].Document.NodeID != "lexical-a" || got[2].Document.NodeID != "lexical-b" {
		t.Fatalf("lost channel coverage: %+v", got)
	}
	if ordered[0].Document.NodeID != "lexical-a" {
		t.Fatal("mutated caller pool")
	}
	ordered[0].BaseSemanticScore = .9
	got = balanceRerankChannels(ordered, 3)
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c.Document.NodeID] {
			t.Fatal("overlap duplicated candidate")
		}
		seen[c.Document.NodeID] = true
	}
}

func TestLexicalTypesFilteredBeforeCandidateBudget(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("segment-%02d", i)
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: id, NodeID: id, UserID: "u", Type: domain.MemoryTypeSegment, Content: "northern telescope expedition", Importance: 1, DecayFactor: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: "fact", NodeID: "fact", UserID: "u", Type: domain.MemoryTypeFact, Content: "telescope", Importance: 1, DecayFactor: 1}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(repo, nil)
	e.SetRerankConfig(RerankConfig{MinCandidates: 2, MaxCandidates: 2})
	got, err := e.Search(ctx, domain.SearchInput{UserID: "u", Query: "northern telescope expedition", TopN: 1})
	if err != nil || len(got.Memories) != 1 || got.Memories[0].ID != "fact" {
		t.Fatalf("hidden segments consumed fact budget: %+v %v", got, err)
	}
}

func TestRerankFallbackAndCancellationAcrossSearchPaths(t *testing.T) {
	for _, path := range []string{"vector", "lexical", "historical"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			repo := memorystore.NewInMemoryMemoryRepository()
			now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
			for _, id := range []string{"a", "b"} {
				if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: id, NodeID: id, UserID: "u", Type: domain.MemoryTypeFact, Content: "owner " + id, Importance: 1, DecayFactor: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
			}
			e := NewEngine(repo, nil)
			e.SetClock(func() time.Time { return now })
			input := domain.SearchInput{UserID: "u", Query: "owner", TopN: 2}
			if path == "vector" {
				e.vectors = fixedRerankVectors{}
			}
			if path == "historical" {
				input.AsOf = &now
			}
			before, err := e.SearchWithVariant(ctx, input, SearchVariantFull)
			if err != nil || len(before.Result.Memories) != 2 {
				t.Fatalf("baseline: %+v %v", before, err)
			}
			e.SetRerankConfig(RerankConfig{Model: "test:model"})
			e.SetReranker(rerankFunc(func(context.Context, domain.RerankInput) ([]domain.RerankResult, error) {
				return []domain.RerankResult{{DocumentID: "a", Score: 1}}, nil
			}))
			after, err := e.SearchWithVariant(ctx, input, SearchVariantFull)
			if err != nil || after.Rerank.Status != "degraded" || after.Rerank.Reason != "incomplete_response" || len(after.Result.Memories) != 2 {
				t.Fatalf("fallback diagnostics: %+v %v", after, err)
			}
			for _, doc := range after.Result.Memories {
				for _, original := range before.Result.Memories {
					if doc.ID == original.ID && doc.EffectiveScore != original.EffectiveScore {
						t.Fatal("partial rerank changed fallback scores")
					}
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			defer cancel()
			e.SetReranker(rerankFunc(func(context.Context, domain.RerankInput) ([]domain.RerankResult, error) {
				cancel()
				return nil, context.Canceled
			}))
			if _, err = e.SearchWithVariant(canceled, input, SearchVariantFull); !errors.Is(err, context.Canceled) {
				t.Fatalf("caller cancellation hidden by fallback: %v", err)
			}
		})
	}
}
