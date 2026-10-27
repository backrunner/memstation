package modelcap

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"memstation/apps/server/internal/domain"
)

// InputPolicy is deployment configuration, keyed by an exact provider:model.
// Unknown models are text-only; model names never imply media capabilities.
type InputPolicy map[string][]string

func ParseInputPolicy(raw string) (InputPolicy, error) {
	p := InputPolicy{}
	if strings.TrimSpace(raw) == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("invalid model input modalities JSON")
	}
	if p == nil {
		return nil, fmt.Errorf("model input modalities must be an object")
	}
	for key, modalities := range p {
		prefix, model, ok := strings.Cut(key, ":")
		if provider, valid := domain.ParseProvider(prefix); !ok || !valid || string(provider) != prefix || strings.TrimSpace(model) == "" || model != strings.TrimSpace(model) || key != strings.TrimSpace(key) {
			return nil, fmt.Errorf("model input modalities require exact provider:model keys")
		}
		seen := map[string]bool{}
		for _, m := range modalities {
			if !slices.Contains([]string{"text", "image", "audio", "video", "file"}, m) || seen[m] {
				return nil, fmt.Errorf("invalid or duplicate input modality")
			}
			seen[m] = true
		}
	}
	return p, nil
}

func (p InputPolicy) Modalities(provider domain.Provider, model string) []string {
	result := []string{"text"}
	for _, m := range p[string(provider)+":"+model] {
		if m != "text" {
			result = append(result, m)
		}
	}
	return result
}

func (p InputPolicy) Supports(provider domain.Provider, model string, kind domain.ContentBlockType) bool {
	return slices.Contains(p.Modalities(provider, model), string(kind))
}
