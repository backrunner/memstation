package domain

import "testing"

func TestParseRerankModelNamespacesAndTags(t *testing.T) {
	for _, tc := range []struct {
		value    string
		provider Provider
		model    string
	}{
		{"openrouter:qwen/model:free", ProviderOpenRouter, "qwen/model:free"},
		{"rerank_compatible:owner/model:Q8_0", ProviderRerankCompatible, "owner/model:Q8_0"},
		{"jina:model", ProviderJina, "model"},
		{"cohere:custom-model", ProviderCohere, "custom-model"},
	} {
		p, m, err := ParseRerankModel(tc.value)
		if err != nil || p != tc.provider || m != tc.model {
			t.Fatalf("%q: %s %s %v", tc.value, p, m, err)
		}
		if _, ok := ParseProvider(string(p)); ok {
			t.Fatalf("rerank-only provider became a chat provider: %s", p)
		}
	}
	for _, value := range []string{"", "unknown:model", "ollama:", "openrouter/", "model", "openrouter/qwen/model:free", "jina_ai:model", "rerank-compatible:local-model"} {
		if _, _, err := ParseRerankModel(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
