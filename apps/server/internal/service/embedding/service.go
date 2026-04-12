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
	"memstation/apps/server/internal/runtimeconfig"
	settingsservice "memstation/apps/server/internal/service/settings"
)

type Service struct {
	client   *http.Client
	resolver provider.ConfigResolver
	settings *settingsservice.Service
	timeout  *runtimeconfig.DurationValue
}

func NewService(resolver provider.ConfigResolver, settings *settingsservice.Service, timeout time.Duration) *Service {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Service{
		client:   &http.Client{},
		resolver: resolver,
		settings: settings,
		timeout:  runtimeconfig.NewDurationValue(timeout),
	}
}

func (s *Service) Embed(ctx context.Context, input domain.EmbeddingInput) (domain.EmbeddingResult, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

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
	modelProvider, modelName := parseModelReference(strings.TrimSpace(input.Model))
	if modelProvider != "" && modelProvider != domain.ProviderOpenAICompatible {
		return settingsservice.EmbeddingRuntime{}, errors.New("embedding model provider must use openai_compatible")
	}
	runtime := settingsservice.EmbeddingRuntime{
		Model:   strings.TrimSpace(modelName),
		BaseURL: strings.TrimSpace(baseConfig.BaseURL),
		APIKey:  strings.TrimSpace(baseConfig.APIKey),
	}

	if s.settings != nil && strings.TrimSpace(input.UserID) != "" {
		snapshot, err := s.settings.SnapshotForUser(ctx, input.UserID)
		if err != nil {
			return settingsservice.EmbeddingRuntime{}, err
		}
		overrideProvider, overrideModel := parseModelReference(strings.TrimSpace(anyString(snapshot["embedding_model"])))
		if overrideProvider != "" && overrideProvider != domain.ProviderOpenAICompatible {
			return settingsservice.EmbeddingRuntime{}, errors.New("embedding model provider must use openai_compatible")
		}
		if runtime.Model == "" {
			runtime.Model = strings.TrimSpace(overrideModel)
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

func (s *Service) SetTimeout(timeout time.Duration) {
	if s == nil {
		return
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if s.timeout == nil {
		s.timeout = runtimeconfig.NewDurationValue(timeout)
		return
	}
	s.timeout.Store(timeout)
}

func (s *Service) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := 60 * time.Second
	if s != nil && s.timeout != nil {
		if loaded := s.timeout.Load(); loaded > 0 {
			timeout = loaded
		}
	}
	return context.WithTimeout(ctx, timeout)
}

func parseModelReference(value string) (domain.Provider, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	for _, separator := range []string{":", "/"} {
		parts := strings.SplitN(value, separator, 2)
		if len(parts) != 2 {
			continue
		}
		if providerName, ok := domain.ParseProvider(parts[0]); ok {
			return providerName, strings.TrimSpace(parts[1])
		}
	}
	return "", value
}

func anyString(value any) string {
	typed, _ := value.(string)
	return typed
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
