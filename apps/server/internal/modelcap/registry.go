package modelcap

import (
	"sort"
	"strings"

	"memstation/apps/server/internal/domain"
)

type Registry struct {
	capabilities []domain.ModelCapability
}

func NewRegistry(defaultProvider domain.Provider, chatModel, summaryModel, analysisModel, embeddingModel string) *Registry {
	capabilities := make([]domain.ModelCapability, 0, 4)
	add := func(modelID, role string, supportsTools, supportsStream bool) {
		if modelID == "" {
			return
		}
		providerName, resolvedModelID := splitModelReference(modelID)
		if providerName == "" {
			providerName = defaultProvider
		}
		if resolvedModelID == "" {
			resolvedModelID = modelID
		}
		capabilities = append(capabilities, domain.ModelCapability{
			InputModalities: []string{"text"},
			ID:              resolvedModelID,
			Provider:        providerName,
			Role:            role,
			SupportsTools:   supportsTools,
			SupportsStream:  supportsStream,
		})
	}
	add(chatModel, "chat", true, true)
	add(summaryModel, "summary", false, false)
	add(analysisModel, "analysis", true, false)
	add(embeddingModel, "embedding", false, false)
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Role < capabilities[j].Role })
	return &Registry{capabilities: capabilities}
}

func (r *Registry) List() []domain.ModelCapability {
	out := make([]domain.ModelCapability, len(r.capabilities))
	copy(out, r.capabilities)
	for i := range out {
		out[i].InputModalities = append([]string(nil), out[i].InputModalities...)
	}
	return out
}

// SetInputPolicy is called during assembly, before the registry is published.
func (r *Registry) SetInputPolicy(policy InputPolicy) {
	for i := range r.capabilities {
		c := &r.capabilities[i]
		c.InputModalities = policy.Modalities(c.Provider, c.ID)
	}
}

func splitModelReference(value string) (domain.Provider, string) {
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
