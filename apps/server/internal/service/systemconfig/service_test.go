package systemconfig

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/runtimeconfig"
)

func TestBootstrapRejectsPersistedCrossSiteCookieWithoutSecureFlag(t *testing.T) {
	t.Parallel()

	store := runtimeconfig.NewFileStore(filepath.Join(t.TempDir(), "runtime.json"))
	_, err := store.Update(context.Background(), func(state *runtimeconfig.State) error {
		state.System["session_cookie_cross_site"] = json.RawMessage("true")
		return nil
	})
	if err != nil {
		t.Fatalf("seed runtime state: %v", err)
	}

	service := NewService(store, DefaultsFromConfig(validSystemConfig(false)))
	_, err = service.Bootstrap(context.Background())
	if err == nil {
		t.Fatal("expected persisted insecure cross-site cookie config to be rejected")
	}
	if !strings.Contains(err.Error(), "session_cookie_secure must be true") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateRejectsCrossSiteCookieWithoutSecureFlag(t *testing.T) {
	t.Parallel()

	service := NewService(nil, DefaultsFromConfig(validSystemConfig(false)))
	if _, err := service.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap system config: %v", err)
	}

	_, err := service.Update(context.Background(), map[string]any{
		"session_cookie_cross_site": true,
	})
	if err == nil {
		t.Fatal("expected insecure cross-site cookie update to be rejected")
	}
	if !strings.Contains(err.Error(), "session_cookie_secure must be true") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateAllowsCrossSiteCookieWithSecureFlag(t *testing.T) {
	t.Parallel()

	service := NewService(nil, DefaultsFromConfig(validSystemConfig(true)))
	if _, err := service.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap system config: %v", err)
	}

	if _, err := service.Update(context.Background(), map[string]any{"session_cookie_cross_site": true}); err != nil {
		t.Fatalf("expected secure cross-site cookie update to succeed: %v", err)
	}
	if !service.LiveSnapshot().SessionCookieCrossSite {
		t.Fatal("expected cross-site cookie flag to be live")
	}
}

func validSystemConfig(sessionCookieSecure bool) bootstrap.Config {
	return bootstrap.Config{
		VectorStoreBackend:        "pgvector",
		ProviderTimeout:           time.Second,
		MaxContextTokens:          4096,
		MaxInjectionTokens:        1024,
		RerankCandidateMultiplier: 4,
		RerankMinCandidates:       8,
		RerankMaxCandidates:       16,
		CacheTTL:                  time.Minute,
		SessionCookieSecure:       sessionCookieSecure,
		SessionCookieCrossSite:    false,
		Multimodal: bootstrap.MultimodalConfig{
			StorageBackend:   "local",
			StorageLocalRoot: "./data/blobs",
		},
	}
}
