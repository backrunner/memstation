package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/runtimeconfig"
)

type Service struct {
	client       *http.Client
	resolver     provider.ConfigResolver
	defaultModel string
	timeout      *runtimeconfig.DurationValue
}

func NewService(resolver provider.ConfigResolver, defaultModel string, timeout time.Duration) *Service {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Service{
		client:       &http.Client{},
		resolver:     resolver,
		defaultModel: strings.TrimSpace(defaultModel),
		timeout:      runtimeconfig.NewDurationValue(timeout),
	}
}

func (s *Service) Rerank(ctx context.Context, input domain.RerankInput) ([]domain.RerankResult, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if len(input.Documents) == 0 {
		return nil, nil
	}
	runtime, err := s.resolveRuntime(ctx, input.Model)
	if err != nil {
		return nil, err
	}

	limit := input.TopN
	if limit <= 0 || limit > len(input.Documents) {
		limit = len(input.Documents)
	}
	documents := make([]string, 0, len(input.Documents))
	for _, item := range input.Documents {
		documents = append(documents, strings.TrimSpace(item.Text))
	}

	endpoint := strings.TrimRight(runtime.BaseURL, "/") + "/rerank"
	raw, _, err := provider.DoJSONRequest(
		ctx,
		s.client,
		runtime.Provider,
		http.MethodPost,
		endpoint,
		map[string]string{
			"Authorization": "Bearer " + runtime.APIKey,
		},
		map[string]any{
			"model":     runtime.Model,
			"query":     strings.TrimSpace(input.Query),
			"documents": documents,
			"top_n":     limit,
		},
	)
	if err != nil {
		return nil, err
	}

	type rerankItem struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	}
	type rerankResponse struct {
		Results []rerankItem `json:"results"`
		Data    struct {
			Results []rerankItem `json:"results"`
		} `json:"data"`
	}
	var response rerankResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	results := response.Results
	if len(results) == 0 {
		results = response.Data.Results
	}

	out := make([]domain.RerankResult, 0, len(results))
	for _, item := range results {
		if item.Index < 0 || item.Index >= len(input.Documents) {
			continue
		}
		out = append(out, domain.RerankResult{
			DocumentID: input.Documents[item.Index].ID,
			Index:      item.Index,
			Score:      item.RelevanceScore,
		})
	}
	return out, nil
}

type runtime struct {
	Provider domain.Provider
	Model    string
	BaseURL  string
	APIKey   string
}

func (s *Service) resolveRuntime(ctx context.Context, overrideModel string) (runtime, error) {
	if s.resolver == nil {
		return runtime{}, errors.New("rerank config resolver is not configured")
	}
	modelRef := firstNonEmpty(strings.TrimSpace(overrideModel), s.defaultModel)
	providerName, modelName, err := parseModelReference(modelRef)
	if err != nil {
		return runtime{}, err
	}
	cfg, err := s.resolver.Resolve(ctx, providerName)
	if err != nil {
		return runtime{}, err
	}
	result := runtime{
		Provider: providerName,
		Model:    modelName,
		BaseURL:  strings.TrimSpace(cfg.BaseURL),
		APIKey:   strings.TrimSpace(cfg.APIKey),
	}
	switch {
	case result.Model == "":
		return runtime{}, errors.New("rerank model is not configured")
	case result.BaseURL == "":
		return runtime{}, errors.New("rerank endpoint is not configured")
	case result.APIKey == "":
		return runtime{}, errors.New("rerank api key is not configured")
	default:
		return result, nil
	}
}

func parseModelReference(value string) (domain.Provider, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", errors.New("rerank model is not configured")
	}
	for _, separator := range []string{":", "/"} {
		parts := strings.SplitN(value, separator, 2)
		if len(parts) != 2 {
			continue
		}
		providerName := domain.NormalizeProvider(parts[0])
		if !slices.Contains(domain.RerankProviders(), providerName) {
			continue
		}
		modelName := strings.TrimSpace(parts[1])
		if modelName == "" {
			return "", "", errors.New("rerank model name is required")
		}
		return providerName, modelName, nil
	}
	return "", "", errors.New("rerank model must include a provider prefix like cohere:model or jina:model")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

var _ ports.Reranker = (*Service)(nil)
