package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/security"
	settingsservice "memstation/apps/server/internal/service/settings"
)

func TestServiceEmbedsWithOpenAICompatibleEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-global" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["model"]; got != "text-embedding-3-large" {
			t.Fatalf("unexpected model: %v", got)
		}
		if got := body["input"]; got != "hello world" {
			t.Fatalf("unexpected input: %v", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "text-embedding-3-large",
			"data": []map[string]any{{
				"embedding": []float32{0.1, 0.2, 0.3, 0.4},
			}},
			"usage": map[string]any{
				"prompt_tokens": 8,
			},
		})
	}))
	defer server.Close()

	service := NewService(provider.StaticConfigResolver{
		domain.ProviderOpenAI: {
			APIKey:  "sk-global",
			BaseURL: server.URL + "/v1",
		},
	}, settingsservice.NewService(nil, nil, "", "", "", "text-embedding-3-large", 2048), time.Second)

	result, err := service.Embed(context.Background(), domain.EmbeddingInput{
		UserID: "user-1",
		Text:   "hello world",
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if result.Model != "text-embedding-3-large" {
		t.Fatalf("unexpected result model: %s", result.Model)
	}
	if result.PromptTokens != 8 {
		t.Fatalf("unexpected prompt tokens: %d", result.PromptTokens)
	}
	if len(result.Vector) != 4 {
		t.Fatalf("unexpected vector size: %d", len(result.Vector))
	}
}

func TestServicePrefersUserEmbeddingModelOverride(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-embed" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["model"]; got != "text-embedding-3-small" {
			t.Fatalf("unexpected model: %v", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "text-embedding-3-small",
			"data": []map[string]any{{
				"embedding": []float32{0.9, 0.8, 0.7},
			}},
		})
	}))
	defer server.Close()

	encryptor, err := security.NewAESGCMEncryptor("embedding-test-master-key")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	settings := settingsservice.NewService(nil, encryptor, "", "", "", "text-embedding-3-large", 2048)
	if _, err := settings.UpdateForUser(context.Background(), "user-1", map[string]any{
		"embedding_model": "text-embedding-3-small",
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	service := NewService(provider.StaticConfigResolver{
		domain.ProviderOpenAICompatible: {
			APIKey:  "sk-embed",
			BaseURL: server.URL,
		},
	}, settings, time.Second)

	result, err := service.Embed(context.Background(), domain.EmbeddingInput{
		UserID: "user-1",
		Text:   "remember me",
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if result.Model != "text-embedding-3-small" {
		t.Fatalf("unexpected result model: %s", result.Model)
	}
	if len(result.Vector) != 3 {
		t.Fatalf("unexpected vector size: %d", len(result.Vector))
	}
}
