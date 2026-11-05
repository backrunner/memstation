package memory

import (
	"context"
	"reflect"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

type supersessionReadRepository struct {
	ports.MemoryRepository
	reads      [][]string
	lineageKey string
}

func (r *supersessionReadRepository) ListDocumentsByNodeIDsInGeneration(_ context.Context, _, _ string, ids []string) ([]domain.MemoryDocument, error) {
	r.reads = append(r.reads, append([]string(nil), ids...))
	return []domain.MemoryDocument{{ID: "old", NodeID: "old-node", Type: domain.MemoryTypeFact, GenerationID: "gen", Metadata: map[string]any{"lineage_group_id": "fresh-lineage"}}}, nil
}

func (r *supersessionReadRepository) ListFactLineageByKey(_ context.Context, _, _, key string) (domain.FactLineage, error) {
	r.lineageKey = key
	return domain.FactLineage{CurrentHeadID: "new", LineageGroupID: key, Entries: []domain.FactLineageEntry{{FactID: "old", NodeID: "old-node"}, {FactID: "new", NodeID: "new-node"}}}, nil
}

func TestSupersessionReadsOnlyFactsAndUsesFreshLineage(t *testing.T) {
	repo := &supersessionReadRepository{}
	engine := NewEngine(repo, nil)
	input := domain.SearchInput{UserID: "owner", GenerationID: "gen"}
	docs := []domain.MemoryDocument{
		{ID: "old", NodeID: "old-node", Type: domain.MemoryTypeFact, Metadata: map[string]any{"lineage_group_id": "stale-lineage"}},
		{NodeID: "summary", Type: domain.MemoryTypeSummary},
		{NodeID: "segment", Type: domain.MemoryTypeSegment},
		{NodeID: "excluded-fact", Type: domain.MemoryTypeFact},
	}
	scores := map[string]float64{"old-node": .9, "summary": .8, "segment": .7}
	got, _, lineage, err := engine.canonicalizeSupersededScores(context.Background(), input, scores, docs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repo.reads, [][]string{{"old-node"}}) || repo.lineageKey != "fresh-lineage" || lineage["new-node"].LineageGroupID != "fresh-lineage" {
		t.Fatalf("incorrect freshness or hydration scope: %+v, %v", repo, lineage)
	}
	if _, exists := got["old-node"]; exists || got["new-node"] != .9 || got["summary"] != .8 || got["segment"] != .7 || len(got) != 3 {
		t.Fatalf("canonical scores changed: %v", got)
	}
	repo.reads = nil
	nonFactScores := map[string]float64{"summary": .8, "segment": .7}
	got, _, _, err = engine.canonicalizeSupersededScores(context.Background(), input, nonFactScores, docs[1:3])
	if err != nil || len(repo.reads) != 0 || !reflect.DeepEqual(got, nonFactScores) {
		t.Fatalf("non-fact candidates rehydrated: %v, %v", repo.reads, err)
	}
	// A previously missing candidate can become visible during the request.
	// Unknown types must still be fetched and canonicalized to their current head.
	got, _, _, err = engine.canonicalizeSupersededScores(context.Background(), input, scores, docs[1:3])
	if err != nil || !reflect.DeepEqual(repo.reads, [][]string{{"old-node"}}) || got["new-node"] != .9 {
		t.Fatalf("unknown candidate freshness lost: %v, %v, %v", got, repo.reads, err)
	}
}

type revokedCandidateRepository struct {
	ports.MemoryRepository
	reads int
}

func (r *revokedCandidateRepository) Search(_ context.Context, input domain.SearchInput) (domain.SearchResult, error) {
	return domain.SearchResult{Memories: []domain.MemoryDocument{{ID: "source", NodeID: "source-node", UserID: input.UserID, GenerationID: input.GenerationID, Type: domain.MemoryTypeSummary, Content: "Previously visible content", Score: .9}}}, nil
}

func (r *revokedCandidateRepository) ListDocumentsByNodeIDsInGeneration(_ context.Context, _, _ string, _ []string) ([]domain.MemoryDocument, error) {
	r.reads++
	return nil, nil // Deleted or made inaccessible after initial recall.
}

func TestNonFactCandidatesAreStillRevalidatedBeforeRanking(t *testing.T) {
	repo := &revokedCandidateRepository{}
	engine := NewEngine(repo, nil)
	found, err := engine.SearchWithVariant(context.Background(), domain.SearchInput{UserID: "owner", GenerationID: "gen", TopN: 5}, SearchVariantFull)
	if err != nil || len(found.Candidates) != 0 || len(found.Result.Memories) != 0 || repo.reads != 1 {
		t.Fatalf("revoked candidate was reused: %v, reads=%d, error=%v", found, repo.reads, err)
	}
}
