package modelcap

import (
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestExactModelInputPolicy(t *testing.T) {
	p, err := ParseInputPolicy(`{"openai_compatible:vision:tag":["image"],"gemini:omni":["text","audio","video"]}`)
	if err != nil || !p.Supports(domain.ProviderOpenAICompatible, "vision:tag", domain.ContentBlockImage) || p.Supports(domain.ProviderAnthropic, "vision:tag", domain.ContentBlockImage) || p.Supports(domain.ProviderOpenAICompatible, "unknown", domain.ContentBlockImage) {
		t.Fatal(p, err)
	}
	for _, raw := range []string{`null`, `[]`, `{"vision":["image"]}`, `{"openai_compatible:x":["magic"]}`, `{"openai_compatible:x":["image","image"]}`} {
		if _, err := ParseInputPolicy(raw); err == nil {
			t.Fatal("invalid policy accepted", raw)
		}
	}
}
