package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
	settingsservice "memstation/apps/server/internal/service/settings"
)

type Service struct {
	client   *http.Client
	resolver provider.ConfigResolver
	settings *settingsservice.Service
}

func NewService(resolver provider.ConfigResolver, settings *settingsservice.Service, timeout time.Duration) *Service {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Service{
		client:   &http.Client{Timeout: timeout},
		resolver: resolver,
		settings: settings,
	}
}

func (s *Service) Embed(ctx context.Context, input domain.EmbeddingInput) (domain.EmbeddingResult, error) {
	runtime, err := s.resolveRuntime(ctx, input)
	if err != nil {
		return domain.EmbeddingResult{}, err
	}

	raw, _, err := provider.DoJSONRequest(
		ctx,
		s.client,
		domain.ProviderOpenAI,
		http.MethodPost,
		strings.TrimRight(runtime.BaseURL, "/")+"/embeddings",
		map[string]string{
			"Authorization": "Bearer " + runtime.APIKey,
		},
		map[string]any{
			"model": runtime.Model,
			"input": input.Text,
		},
	)
	if err != nil {
		return domain.EmbeddingResult{}, err
	}

	var response struct {
		Model string `json:"model"`
		Data  []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return domain.EmbeddingResult{}, err
	}
	if len(response.Data) == 0 || len(response.Data[0].Embedding) == 0 {
		return domain.EmbeddingResult{}, errors.New("embedding provider returned an empty vector")
	}

	vector := response.Data[0].Embedding
	return domain.EmbeddingResult{
		Vector:       vector,
		Model:        firstNonEmpty(strings.TrimSpace(response.Model), runtime.Model),
		PromptTokens: response.Usage.PromptTokens,
		Dimensions:   len(vector),
	}, nil
}

func (s *Service) resolveRuntime(ctx context.Context, input domain.EmbeddingInput) (settingsservice.EmbeddingRuntime, error) {
	if s.resolver == nil {
		return settingsservice.EmbeddingRuntime{}, errors.New("embedding config resolver is not configured")
	}

	baseConfig, err := s.resolver.Resolve(ctx, domain.ProviderOpenAI)
	if err != nil {
		return settingsservice.EmbeddingRuntime{}, err
	}
	runtime := settingsservice.EmbeddingRuntime{
		Model:   strings.TrimSpace(input.Model),
		BaseURL: strings.TrimSpace(baseConfig.BaseURL),
		APIKey:  strings.TrimSpace(baseConfig.APIKey),
	}

	if s.settings != nil && strings.TrimSpace(input.UserID) != "" {
		override, err := s.settings.ResolveEmbeddingRuntime(ctx, input.UserID)
		if err != nil {
			return settingsservice.EmbeddingRuntime{}, err
		}
		if runtime.Model == "" {
			runtime.Model = strings.TrimSpace(override.Model)
		}
		if strings.TrimSpace(override.BaseURL) != "" {
			runtime.BaseURL = strings.TrimSpace(override.BaseURL)
		}
		if override.HasUserAPIKey {
			runtime.APIKey = strings.TrimSpace(override.APIKey)
		}
	}

	switch {
	case runtime.Model == "":
		return settingsservice.EmbeddingRuntime{}, errors.New("embedding model is not configured")
	case runtime.BaseURL == "":
		return settingsservice.EmbeddingRuntime{}, errors.New("embedding endpoint is not configured")
	case runtime.APIKey == "":
		return settingsservice.EmbeddingRuntime{}, errors.New("embedding api key is not configured")
	default:
		return runtime, nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ ports.EmbeddingClient = (*Service)(nil)
