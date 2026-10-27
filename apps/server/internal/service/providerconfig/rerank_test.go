package providerconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
)

func TestRerankConfigurationPersistsAndIsScoped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.json")
	store := runtimeconfig.NewFileStore(path)
	encryptor, err := security.NewAESGCMEncryptor("test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	defaults := Defaults{ScopeRerank: {
		domain.ProviderOpenRouter: provider.RuntimeConfig{BaseURL: "https://openrouter.ai/api/v1"},
	}}
	s := NewService(store, encryptor, defaults)
	endpoint, key := "http://127.0.0.1:18082", "test-private-rerank-key"
	if _, err := s.Update(ctx, ScopeRerank, domain.ProviderRerankCompatible, UpdateInput{BaseURL: &endpoint}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, ScopeRerank, domain.ProviderOpenRouter, UpdateInput{APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatal("plaintext key persisted")
	}
	restored := NewService(store, encryptor, defaults)
	if err := restored.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	local, err := restored.Resolve(ctx, ScopeRerank, domain.ProviderRerankCompatible)
	if err != nil || local.BaseURL != endpoint || local.APIKey != "" {
		t.Fatalf("local configuration: %+v %v", local, err)
	}
	remote, err := restored.Resolve(ctx, ScopeRerank, domain.ProviderOpenRouter)
	if err != nil || remote.APIKey != key {
		t.Fatal("remote credential did not roundtrip")
	}
	if _, err := restored.Resolve(ctx, ScopeGateway, domain.ProviderOpenRouter); err == nil {
		t.Fatal("rerank provider leaked into gateway")
	}
	diagnostics, err := restored.Diagnostics(ctx, map[Scope]domain.Provider{ScopeRerank: domain.ProviderRerankCompatible}, map[Scope]domain.Provider{ScopeRerank: domain.ProviderRerankCompatible}, 60, 60)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range diagnostics {
		if item.Scope == ScopeRerank && item.Provider == domain.ProviderRerankCompatible && (len(item.Issues) != 0 || item.Status != "ready" || !item.IsDesiredDefault || !item.IsLiveDefault) {
			t.Fatalf("keyless local endpoint should be usable: %+v", item)
		}
	}
}
