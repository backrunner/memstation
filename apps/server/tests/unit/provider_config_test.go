package unit_test

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/security"
	providerconfigservice "memstation/apps/server/internal/service/providerconfig"
)

func TestProviderConfigServiceUpdateAndResolve(t *testing.T) {
	t.Parallel()
	encryptor, err := security.NewAESGCMEncryptor("provider-config-test-master-key")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	service := providerconfigservice.NewService(nil, encryptor, map[domain.Provider]provider.RuntimeConfig{
		domain.ProviderOpenAI: {
			APIKey:  "env-key",
			BaseURL: "https://api.openai.com/v1",
		},
	})

	baseURL := "https://proxy.example.com/openai"
	apiKey := "sk-live"
	if _, err := service.Update(context.Background(), domain.ProviderOpenAI, providerconfigservice.UpdateInput{
		APIKey:  &apiKey,
		BaseURL: &baseURL,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	resolved, err := service.Resolve(context.Background(), domain.ProviderOpenAI)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.APIKey != apiKey {
		t.Fatalf("expected decrypted api key, got %s", resolved.APIKey)
	}
	if resolved.BaseURL != baseURL {
		t.Fatalf("expected updated base url, got %s", resolved.BaseURL)
	}
}
