package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

func TestNormalizeEmbeddingOptionsDeterministicDefaults(t *testing.T) {
	t.Parallel()

	options, err := normalizeEmbeddingOptions(EmbeddingOptions{})
	if err != nil {
		t.Fatalf("normalize deterministic options: %v", err)
	}
	if options.Mode != EmbeddingModeDeterministic {
		t.Fatalf("expected deterministic mode, got %s", options.Mode)
	}
	if options.Model == "" {
		t.Fatal("expected deterministic embedding mode to inject a default model label")
	}
}

func TestNormalizeEmbeddingOptionsProviderRequiresConfig(t *testing.T) {
	t.Parallel()

	_, err := normalizeEmbeddingOptions(EmbeddingOptions{Mode: EmbeddingModeProvider})
	if err == nil {
		t.Fatal("expected provider embedding mode to require explicit config")
	}
}

func TestRunBenchmarkProviderModeUsesEmbeddingEndpoint(t *testing.T) {
	t.Parallel()

	type requestRecord struct {
		Model string
		Input string
	}

	var (
		mu       sync.Mutex
		requests []requestRecord
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-eval" {
			t.Fatalf("unexpected auth header: %s", got)
		}

		var body struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		mu.Lock()
		requests = append(requests, requestRecord{
			Model: body.Model,
			Input: body.Input,
		})
		mu.Unlock()

		vector := []float32{0, 1, 0}
		if body.Input == "alpha project active generation" || body.Input == "alpha project query" {
			vector = []float32{1, 0, 0}
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"model": body.Model,
			"data": []map[string]any{{
				"embedding": vector,
			}},
			"usage": map[string]any{
				"prompt_tokens": 4,
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	dataset := Dataset{
		Name: "provider-mode",
		Documents: []DatasetDocument{
			{
				ID:           "doc-active",
				NodeID:       "node-z",
				GenerationID: "gen-a",
				UserID:       "user-1",
				Type:         domain.MemoryTypeSummary,
				Content:      "alpha project active generation",
			},
			{
				ID:           "doc-inactive",
				NodeID:       "node-a",
				GenerationID: "gen-b",
				UserID:       "user-1",
				Type:         domain.MemoryTypeSummary,
				Content:      "alpha project inactive generation",
			},
		},
		Queries: []QueryCase{
			{
				CaseID:       "q1",
				UserID:       "user-1",
				GenerationID: "gen-a",
				Query:        "alpha project query",
				TopN:         1,
				Positives:    []string{"doc-active"},
				Negatives:    []string{"doc-inactive"},
			},
		},
	}

	report, err := RunBenchmark(context.Background(), dataset, RunOptions{
		Variants: []memoryservice.SearchVariant{memoryservice.SearchVariantSemanticOnly},
		TopN:     1,
		Embedding: EmbeddingOptions{
			Mode:    EmbeddingModeProvider,
			Model:   "text-embedding-3-small",
			BaseURL: server.URL + "/v1",
			APIKey:  "sk-eval",
		},
	})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}

	if report.Embedding.Mode != EmbeddingModeProvider {
		t.Fatalf("expected provider mode, got %s", report.Embedding.Mode)
	}
	if report.Embedding.Model != "text-embedding-3-small" {
		t.Fatalf("unexpected report model: %s", report.Embedding.Model)
	}
	if report.Embedding.BaseURL != server.URL {
		t.Fatalf("expected sanitized base url %s, got %s", server.URL, report.Embedding.BaseURL)
	}
	if len(report.Variants) != 1 {
		t.Fatalf("expected one variant, got %d", len(report.Variants))
	}
	if got := report.Variants[0].Aggregate.RecallAt1; got != 1 {
		t.Fatalf("expected recall@1 to be 1, got %.3f", got)
	}
	if got := len(report.Variants[0].Cases[0].Results); got != 1 {
		t.Fatalf("expected one result, got %d", got)
	}
	if got := report.Variants[0].Cases[0].Results[0].Document.ID; got != "doc-active" {
		t.Fatalf("expected active-generation document, got %s", got)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(requests) != 3 {
		t.Fatalf("expected 3 embedding requests (2 docs + 1 query), got %d", len(requests))
	}
	for _, request := range requests {
		if request.Model != "text-embedding-3-small" {
			t.Fatalf("unexpected request model: %s", request.Model)
		}
	}
}
