package eval

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
	embeddingservice "memstation/apps/server/internal/service/embedding"
)

type EmbeddingMode string

const (
	EmbeddingModeDeterministic EmbeddingMode = "deterministic"
	EmbeddingModeProvider      EmbeddingMode = "provider"
)

type EmbeddingOptions struct {
	Mode     EmbeddingMode `json:"mode,omitempty"`
	Provider string        `json:"provider,omitempty"`
	Model    string        `json:"model,omitempty"`
	BaseURL  string        `json:"base_url,omitempty"`
	APIKey   string        `json:"-"`
	Timeout  time.Duration `json:"-"`
}

type EmbeddingRunInfo struct {
	Mode     EmbeddingMode `json:"mode"`
	Provider string        `json:"provider,omitempty"`
	Model    string        `json:"model"`
	BaseURL  string        `json:"base_url,omitempty"`
}

func normalizeEmbeddingOptions(options EmbeddingOptions) (EmbeddingOptions, error) {
	mode := EmbeddingMode(strings.TrimSpace(string(options.Mode)))
	if mode == "" {
		mode = EmbeddingModeDeterministic
	}
	options.Mode = mode
	options.Provider = firstNonEmptyLiteral(strings.TrimSpace(options.Provider), string(domain.ProviderOpenAICompatible))
	options.Model = strings.TrimSpace(options.Model)
	options.BaseURL = strings.TrimSpace(options.BaseURL)
	options.APIKey = strings.TrimSpace(options.APIKey)
	if options.Timeout <= 0 {
		options.Timeout = 60 * time.Second
	}

	switch options.Mode {
	case EmbeddingModeDeterministic:
		if options.Model == "" {
			options.Model = "benchmark-local-deterministic"
		}
		options.BaseURL = ""
		options.APIKey = ""
		return options, nil
	case EmbeddingModeProvider:
		switch {
		case options.Model == "":
			return EmbeddingOptions{}, fmt.Errorf("provider embedding mode requires an embedding model")
		case options.BaseURL == "":
			return EmbeddingOptions{}, fmt.Errorf("provider embedding mode requires an embedding base url")
		case options.APIKey == "":
			return EmbeddingOptions{}, fmt.Errorf("provider embedding mode requires an embedding api key")
		default:
			return options, nil
		}
	default:
		return EmbeddingOptions{}, fmt.Errorf("unsupported embedding mode %q", options.Mode)
	}
}

func (o EmbeddingOptions) runInfo() EmbeddingRunInfo {
	baseURL := strings.TrimSpace(o.BaseURL)
	if parsed, err := url.Parse(baseURL); err == nil {
		if parsed.Scheme != "" && parsed.Host != "" {
			baseURL = parsed.Scheme + "://" + parsed.Host
		}
	}
	info := EmbeddingRunInfo{
		Mode:     o.Mode,
		Provider: o.Provider,
		Model:    o.Model,
	}
	if o.Mode == EmbeddingModeProvider {
		info.BaseURL = baseURL
	}
	return info
}

func newEvalEmbeddingClient(options EmbeddingOptions) (ports.EmbeddingClient, error) {
	switch options.Mode {
	case EmbeddingModeDeterministic:
		return nil, nil
	case EmbeddingModeProvider:
		resolver := provider.StaticConfigResolver{
			domain.ProviderOpenAICompatible: {
				APIKey:  options.APIKey,
				BaseURL: options.BaseURL,
			},
		}
		return embeddingClientWithDefaultModel{
			delegate:     embeddingservice.NewService(resolver, nil, options.Timeout),
			defaultModel: options.Model,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported embedding mode %q", options.Mode)
	}
}

type embeddingClientWithDefaultModel struct {
	delegate     ports.EmbeddingClient
	defaultModel string
}

func (c embeddingClientWithDefaultModel) Embed(ctx context.Context, input domain.EmbeddingInput) (domain.EmbeddingResult, error) {
	if strings.TrimSpace(input.Model) == "" {
		input.Model = c.defaultModel
	}
	return c.delegate.Embed(ctx, input)
}

func firstNonEmptyLiteral(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
