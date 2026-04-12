package unit_test

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	providerconfigservice "memstation/apps/server/internal/service/providerconfig"
)

func TestProviderConfigServiceUpdateAndResolve(t *testing.T) {
	t.Parallel()
	encryptor, err := security.NewAESGCMEncryptor("provider-config-test-master-key")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	service := providerconfigservice.NewService(nil, encryptor, providerconfigservice.Defaults{
		providerconfigservice.ScopeGateway: {
			domain.ProviderOpenAICompatible: {
				APIKey:  "env-key",
				BaseURL: "https://api.openai.com/v1",
			},
		},
	})

	baseURL := "https://proxy.example.com/openai"
	apiKey := "sk-live"
	if _, err := service.Update(context.Background(), providerconfigservice.ScopeGateway, domain.ProviderOpenAICompatible, providerconfigservice.UpdateInput{
		APIKey:  &apiKey,
		BaseURL: &baseURL,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	resolved, err := service.Resolve(context.Background(), providerconfigservice.ScopeGateway, domain.ProviderOpenAICompatible)
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

func TestProviderConfigServiceScopesRemainIndependent(t *testing.T) {
	t.Parallel()
	encryptor, err := security.NewAESGCMEncryptor("provider-config-scope-test-master-key")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	service := providerconfigservice.NewService(nil, encryptor, providerconfigservice.Defaults{
		providerconfigservice.ScopeGateway: {
			domain.ProviderOpenAICompatible: {
				APIKey:  "gateway-default-key",
				BaseURL: "https://gateway.example.com/v1",
			},
		},
		providerconfigservice.ScopeInternal: {
			domain.ProviderOpenAICompatible: {
				APIKey:  "internal-default-key",
				BaseURL: "https://internal.example.com/v1",
			},
		},
	})

	gatewayBaseURL := "https://gateway-override.example.com/v1"
	gatewayAPIKey := "sk-gateway"
	if _, err := service.Update(context.Background(), providerconfigservice.ScopeGateway, domain.ProviderOpenAICompatible, providerconfigservice.UpdateInput{
		APIKey:  &gatewayAPIKey,
		BaseURL: &gatewayBaseURL,
	}); err != nil {
		t.Fatalf("update gateway: %v", err)
	}

	internalResolved, err := service.Resolve(context.Background(), providerconfigservice.ScopeInternal, domain.ProviderOpenAICompatible)
	if err != nil {
		t.Fatalf("resolve internal: %v", err)
	}
	if internalResolved.APIKey != "internal-default-key" {
		t.Fatalf("expected internal scope api key to remain isolated, got %s", internalResolved.APIKey)
	}
	if internalResolved.BaseURL != "https://internal.example.com/v1" {
		t.Fatalf("expected internal scope base url to remain isolated, got %s", internalResolved.BaseURL)
	}
}

func TestProviderConfigServiceSupportsRerankScope(t *testing.T) {
	t.Parallel()

	encryptor, err := security.NewAESGCMEncryptor("provider-config-rerank-test-master-key")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	service := providerconfigservice.NewService(nil, encryptor, providerconfigservice.Defaults{
		providerconfigservice.ScopeRerank: {
			domain.ProviderCohere: {
				APIKey:  "cohere-default-key",
				BaseURL: "https://api.cohere.com/v2",
			},
		},
	})

	resolved, err := service.Resolve(context.Background(), providerconfigservice.ScopeRerank, domain.ProviderCohere)
	if err != nil {
		t.Fatalf("resolve rerank scope: %v", err)
	}
	if resolved.APIKey != "cohere-default-key" {
		t.Fatalf("expected rerank api key, got %s", resolved.APIKey)
	}
	if resolved.BaseURL != "https://api.cohere.com/v2" {
		t.Fatalf("expected rerank base url, got %s", resolved.BaseURL)
	}
}
