package vector

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
)

type mockEmbeddingClient struct {
	results map[string]domain.EmbeddingResult
}

func (s mockEmbeddingClient) Embed(_ context.Context, input domain.EmbeddingInput) (domain.EmbeddingResult, error) {
	key := input.Model + "::" + input.Text
	if result, ok := s.results[key]; ok {
		return result, nil
	}
	return domain.EmbeddingResult{
		Vector: []float32{0.1, 0.1},
		Model:  input.Model,
	}, nil
}

func TestPGVectorStoreUsesEmbedderAndFiltersByModel(t *testing.T) {
	t.Parallel()

	store := NewPGVectorStore(nil)
	store.SetEmbedder(mockEmbeddingClient{
		results: map[string]domain.EmbeddingResult{
			"model-a::alpha memory": {Vector: []float32{1, 0}, Model: "model-a"},
			"model-b::beta memory":  {Vector: []float32{0, 1}, Model: "model-b"},
			"model-a::alpha query":  {Vector: []float32{1, 0}, Model: "model-a"},
		},
	})

	if err := store.Upsert(context.Background(), domain.VectorUpsertRequest{
		UserID:           "user-1",
		NodeID:           "node-a",
		NodeType:         "summary",
		EmbeddingVersion: 1,
		EmbeddingModel:   "model-a",
		Content:          "alpha memory",
	}); err != nil {
		t.Fatalf("upsert model-a: %v", err)
	}
	if err := store.Upsert(context.Background(), domain.VectorUpsertRequest{
		UserID:           "user-1",
		NodeID:           "node-b",
		NodeType:         "summary",
		EmbeddingVersion: 1,
		EmbeddingModel:   "model-b",
		Content:          "beta memory",
	}); err != nil {
		t.Fatalf("upsert model-b: %v", err)
	}

	results, err := store.Search(context.Background(), domain.VectorSearchInput{
		UserID:           "user-1",
		Query:            "alpha query",
		TopK:             5,
		EmbeddingVersion: 1,
		EmbeddingModel:   "model-a",
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].NodeID != "node-a" {
		t.Fatalf("unexpected node id: %s", results[0].NodeID)
	}
}
