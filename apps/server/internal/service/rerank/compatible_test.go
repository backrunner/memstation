package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
)

func TestRemoteRerankProtocols(t *testing.T) {
	for _, p := range []domain.Provider{domain.ProviderOpenRouter, domain.ProviderRerankCompatible} {
		t.Run(string(p), func(t *testing.T) {
			key := ""
			if p == domain.ProviderOpenRouter {
				key = "test-key"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/rerank" || (key == "" && r.Header.Get("Authorization") != "") || (key != "" && r.Header.Get("Authorization") != "Bearer "+key) {
					t.Error("incorrect endpoint/auth")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != "vendor/custom:free" {
					t.Error("model namespace/tag lost")
				}
				_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.85}],"usage":{"total_tokens":100}}`))
			}))
			defer server.Close()
			s := NewService(provider.StaticConfigResolver{p: {BaseURL: server.URL + "/api/v1", APIKey: key}}, string(p)+":vendor/custom:free", time.Second)
			got, err := s.Rerank(context.Background(), domain.RerankInput{Query: "q", Documents: []domain.RerankDocument{{ID: "a", Text: "a"}}})
			if err != nil || len(got) != 1 || got[0].Score != .85 {
				t.Fatalf("remote protocol: %+v %v", got, err)
			}
		})
	}
}

func TestRemoteRerankRequiresCredential(t *testing.T) {
	for _, p := range []domain.Provider{domain.ProviderCohere, domain.ProviderJina, domain.ProviderOpenRouter} {
		s := NewService(provider.StaticConfigResolver{p: {BaseURL: "http://127.0.0.1:1"}}, string(p)+":test", time.Second)
		_, err := s.resolveRuntime(context.Background(), "")
		if err == nil || err.Error() != "rerank api key is not configured" {
			t.Fatalf("%s accepted missing credentials: %v", p, err)
		}
	}
}
