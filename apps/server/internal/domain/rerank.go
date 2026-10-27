package domain

import (
	"errors"
	"slices"
	"strings"
)

// ParseRerankModel preserves model namespaces and tags after the provider prefix.
func ParseRerankModel(value string) (Provider, string, error) {
	value = strings.TrimSpace(value)
	prefix, model, ok := strings.Cut(value, ":")
	provider := NormalizeProvider(prefix)
	if !ok || !slices.Contains(RerankProviders(), provider) {
		return "", "", errors.New("rerank model must use a supported provider prefix: cohere, jina, openrouter or rerank_compatible")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return "", "", errors.New("rerank model name is required")
	}
	return provider, model, nil
}
