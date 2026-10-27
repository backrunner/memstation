package memory

import (
	"context"
	"testing"
	"time"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/aicall"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

type projectedSearchEngine struct {
	ports.MemoryEngine
	document    domain.MemoryDocument
	failureCode string
}

func (e projectedSearchEngine) Search(ctx context.Context, _ domain.SearchInput) (domain.SearchResult, error) {
	if e.failureCode != "" {
		aicall.RecordFailure(ctx, e.failureCode)
	}
	return domain.SearchResult{Memories: []domain.MemoryDocument{e.document}, Total: 1}, nil
}

type liveIdentityRepository struct {
	ports.MemoryRepository
	documents []domain.MemoryDocument
}

func (r liveIdentityRepository) ListDocumentsByNodeIDsInGeneration(context.Context, string, string, []string) ([]domain.MemoryDocument, error) {
	return r.documents, nil
}

func TestHistoricalRecallPreservesProjectionButRequiresLiveIdentity(t *testing.T) {
	now := time.Now().UTC()
	old := domain.MemoryDocument{ID: "fact", NodeID: "node", UserID: "owner", Type: domain.MemoryTypeFact, GenerationID: "generation", Content: "Shanghai", Revision: 1}
	current := old
	current.Content = "Beijing"
	current.Revision = 2
	for _, test := range []struct {
		name          string
		asOf, knownAt *time.Time
		live          []domain.MemoryDocument
		want          int
	}{
		{name: "as_of", asOf: &now, live: []domain.MemoryDocument{current}, want: 1},
		{name: "known_at", knownAt: &now, live: []domain.MemoryDocument{current}, want: 1},
		{name: "current_changed", live: []domain.MemoryDocument{current}},
		{name: "withdrawn_history", asOf: &now},
		{name: "unchanged_current", live: []domain.MemoryDocument{old}, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(projectedSearchEngine{document: old}, liveIdentityRepository{MemoryRepository: memorystore.NewInMemoryMemoryRepository(), documents: test.live})
			got, err := service.Search(context.Background(), domain.SearchInput{UserID: "owner", Query: "where", AsOf: test.asOf, KnownAt: test.knownAt})
			if err != nil || len(got.Memories) != test.want {
				t.Fatalf("recall=%+v err=%v", got, err)
			}
			if test.want > 0 && got.Memories[0].Content != "Shanghai" {
				t.Fatalf("lost historical projection: %+v", got.Memories[0])
			}
		})
	}
	current.Content = old.Content
	service := NewService(projectedSearchEngine{document: old}, liveIdentityRepository{MemoryRepository: memorystore.NewInMemoryMemoryRepository(), documents: []domain.MemoryDocument{current}})
	got, err := service.Search(context.Background(), domain.SearchInput{UserID: "owner", Query: "where"})
	if err != nil || len(got.Memories) != 0 {
		t.Fatalf("revision changed during current recall: %+v %v", got, err)
	}
}

func TestRecallExposesDegradedBrokerResultWithoutDiscardingFallback(t *testing.T) {
	doc := domain.MemoryDocument{ID: "fact", NodeID: "node", UserID: "owner", Type: domain.MemoryTypeFact, Content: "retained fallback", Revision: 1}
	service := NewService(projectedSearchEngine{document: doc, failureCode: "broker_rerank_failed"}, liveIdentityRepository{MemoryRepository: memorystore.NewInMemoryMemoryRepository(), documents: []domain.MemoryDocument{doc}})
	result, err := service.Search(context.Background(), domain.SearchInput{UserID: "owner", Query: "fact"})
	if err != nil || len(result.Memories) != 1 || result.Trace == nil || !result.Trace.Degraded || result.Trace.FailureCode != "broker_rerank_failed" {
		t.Fatalf("degraded result lost: %+v %v", result, err)
	}
}
