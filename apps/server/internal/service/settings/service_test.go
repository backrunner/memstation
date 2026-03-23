package settings

import (
	"context"
	"testing"
	"time"

	"memstation/apps/server/internal/cache"
	"memstation/apps/server/internal/security"
)

func TestServiceUsesReadThroughCache(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := cache.NewJSONStore(ctx, nil, time.Minute)
	if err != nil {
		t.Fatalf("new cache store: %v", err)
	}
	defer func() { _ = store.Close(context.Background()) }()

	service := NewService(nil, nil, "gpt-5", "gpt-5-mini", "gpt-5-mini", "text-embedding-3-large", 2048)
	service.SetCache(store)

	if _, err := service.UpdateForUser(ctx, "user-1", map[string]any{
		"chat_model": "claude-sonnet",
	}); err != nil {
		t.Fatalf("update user settings: %v", err)
	}

	snapshot, err := service.SnapshotForUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if got := snapshot["chat_model"]; got != "claude-sonnet" {
		t.Fatalf("expected claude-sonnet, got %v", got)
	}

	service.mu.Lock()
	service.users["user-1"] = Snapshot{
		ChatModel:          "gpt-5",
		SummaryModel:       "gpt-5-mini",
		AnalysisModel:      "gpt-5-mini",
		EmbeddingModel:     "text-embedding-3-large",
		MaxInjectionTokens: 2048,
	}
	service.mu.Unlock()

	cached, err := service.SnapshotForUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("load cached snapshot: %v", err)
	}
	if got := cached["chat_model"]; got != "claude-sonnet" {
		t.Fatalf("expected cached value claude-sonnet, got %v", got)
	}

	updated, err := service.UpdateForUser(ctx, "user-1", map[string]any{
		"chat_model": "gpt-5",
	})
	if err != nil {
		t.Fatalf("update cached user settings: %v", err)
	}
	if got := updated["chat_model"]; got != "gpt-5" {
		t.Fatalf("expected refreshed value gpt-5, got %v", got)
	}
}

func TestResolveEmbeddingRuntimeTracksCustomEndpointAndSecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	encryptor, err := security.NewAESGCMEncryptor("settings-test-master-key")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	service := NewService(nil, encryptor, "gpt-5", "gpt-5-mini", "gpt-5-mini", "text-embedding-3-large", 2048)
	updated, err := service.UpdateForUser(ctx, "user-1", map[string]any{
		"embedding_model":    "text-embedding-3-small",
		"embedding_base_url": "https://embeddings.example.com/v1",
		"embedding_api_key":  "sk-user-override",
	})
	if err != nil {
		t.Fatalf("update embedding settings: %v", err)
	}
	if got := updated["embedding_api_key_configured"]; got != true {
		t.Fatalf("expected embedding api key to be marked configured, got %v", got)
	}

	runtime, err := service.ResolveEmbeddingRuntime(ctx, "user-1")
	if err != nil {
		t.Fatalf("resolve embedding runtime: %v", err)
	}
	if runtime.Model != "text-embedding-3-small" {
		t.Fatalf("unexpected embedding model: %s", runtime.Model)
	}
	if runtime.BaseURL != "https://embeddings.example.com/v1" {
		t.Fatalf("unexpected embedding base url: %s", runtime.BaseURL)
	}
	if runtime.APIKey != "sk-user-override" {
		t.Fatalf("unexpected embedding api key: %s", runtime.APIKey)
	}

	cleared, err := service.UpdateForUser(ctx, "user-1", map[string]any{
		"embedding_base_url":      "",
		"clear_embedding_api_key": true,
	})
	if err != nil {
		t.Fatalf("clear embedding settings: %v", err)
	}
	if got := cleared["embedding_base_url"]; got != "" {
		t.Fatalf("expected embedding base url to clear, got %v", got)
	}
	if got := cleared["embedding_api_key_configured"]; got != false {
		t.Fatalf("expected embedding api key to clear, got %v", got)
	}
}

func TestServiceSnapshotAndUpdateUseAdminAlias(t *testing.T) {
	t.Parallel()

	service := NewService(nil, nil, "gpt-5", "gpt-5-mini", "gpt-5-mini", "text-embedding-3-large", 2048)

	initial := service.Snapshot()
	if got := initial["chat_model"]; got != "gpt-5" {
		t.Fatalf("unexpected default chat model: %v", got)
	}

	updated := service.Update(map[string]any{
		"chat_model":           "claude-sonnet",
		"tool_write_enabled":   true,
		"max_injection_tokens": float64(4096),
	})
	if got := updated["chat_model"]; got != "claude-sonnet" {
		t.Fatalf("unexpected updated chat model: %v", got)
	}
	if got := updated["tool_write_enabled"]; got != true {
		t.Fatalf("expected tool writes enabled, got %v", got)
	}
	if got := updated["max_injection_tokens"]; got != 4096 {
		t.Fatalf("unexpected injection token limit: %v", got)
	}

	runtime, err := service.ResolveEmbeddingRuntime(context.Background(), "admin")
	if err != nil {
		t.Fatalf("resolve admin embedding runtime: %v", err)
	}
	if runtime.Model != "text-embedding-3-large" {
		t.Fatalf("unexpected runtime model: %s", runtime.Model)
	}
	if runtime.HasUserAPIKey {
		t.Fatal("did not expect an admin-specific embedding key")
	}
}

func TestHelperConversions(t *testing.T) {
	t.Parallel()

	if !clearEmbeddingAPIKey(map[string]any{"clear_embedding_api_key": true}) {
		t.Fatal("expected clear embedding api key to be true")
	}
	if clearEmbeddingAPIKey(map[string]any{"clear_embedding_api_key": "true"}) {
		t.Fatal("expected non-bool clear flag to fall back to false")
	}
	if got := nullString("  "); got != nil {
		t.Fatalf("expected nil nullString, got %v", got)
	}
	if got := nullString("value"); got != "value" {
		t.Fatalf("unexpected nullString value: %v", got)
	}
	if got := intOrDefault(int32(8), 0); got != 8 {
		t.Fatalf("unexpected int32 conversion: %d", got)
	}
	if got := intOrDefault(int64(9), 0); got != 9 {
		t.Fatalf("unexpected int64 conversion: %d", got)
	}
	if got := intOrDefault(float64(10), 0); got != 10 {
		t.Fatalf("unexpected float conversion: %d", got)
	}
	if got := intOrDefault("oops", 7); got != 7 {
		t.Fatalf("unexpected fallback conversion: %d", got)
	}
	if got := anyInt(float64(11)); got != 11 {
		t.Fatalf("unexpected anyInt conversion: %d", got)
	}
	if got := anyString("value"); got != "value" {
		t.Fatalf("unexpected anyString conversion: %q", got)
	}
	if got := stringValue(42); got != "" {
		t.Fatalf("expected unsupported stringValue to be empty, got %q", got)
	}
}
