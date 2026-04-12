package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
)

func TestServiceRerankCohere(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v2/rerank" {
			t.Fatalf("expected /v2/rerank path, got %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer cohere-test-key" {
			t.Fatalf("expected bearer auth, got %s", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if got := payload["model"]; got != "rerank-v3.5" {
			t.Fatalf("expected cohere model, got %#v", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 1, "relevance_score": 0.91},
				{"index": 0, "relevance_score": 0.22},
			},
		})
	}))
	defer server.Close()

	service := NewService(provider.StaticConfigResolver{
		domain.ProviderCohere: {
			APIKey:  "cohere-test-key",
			BaseURL: server.URL + "/v2",
		},
	}, "cohere:rerank-v3.5", 0)

	results, err := service.Rerank(context.Background(), domain.RerankInput{
		Query: "rollback owner",
		TopN:  2,
		Documents: []domain.RerankDocument{
			{ID: "doc-1", Text: "Alice handles release notes."},
			{ID: "doc-2", Text: "Alice is the rollback owner."},
		},
	})
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].DocumentID != "doc-2" || results[0].Score != 0.91 {
		t.Fatalf("unexpected top result: %+v", results[0])
	}
}

func TestServiceRerankJina(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/rerank" {
			t.Fatalf("expected /v1/rerank path, got %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer jina-test-key" {
			t.Fatalf("expected bearer auth, got %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 0, "relevance_score": 0.88},
			},
		})
	}))
	defer server.Close()

	service := NewService(provider.StaticConfigResolver{
		domain.ProviderJina: {
			APIKey:  "jina-test-key",
			BaseURL: server.URL + "/v1",
		},
	}, "jina:jina-reranker-v2-base-multilingual", 0)

	results, err := service.Rerank(context.Background(), domain.RerankInput{
		Query: "team owner",
		TopN:  1,
		Documents: []domain.RerankDocument{
			{ID: "doc-1", Text: "Alice is the team owner."},
			{ID: "doc-2", Text: "Bob likes tea."},
		},
	})
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].DocumentID != "doc-1" {
		t.Fatalf("expected doc-1 to win, got %+v", results[0])
	}
}
