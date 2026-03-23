package modelcap

import (
	"sort"

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
		capabilities = append(capabilities, domain.ModelCapability{
			ID:             modelID,
			Provider:       defaultProvider,
			Role:           role,
			SupportsTools:  supportsTools,
			SupportsStream: supportsStream,
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
	return out
}
