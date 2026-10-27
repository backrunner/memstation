package domain

import "testing"

func TestProviderIdentifiersRejectRetiredAliases(t *testing.T) {
	for _, value := range []string{"openai", "responses", "response", "messages", "anthropic_messages", "google", "google_genai", "jina_ai", "openai-compatible"} {
		if _, ok := ParseProvider(value); ok {
			t.Fatal("provider alias accepted", value)
		}
	}
	for _, value := range []string{"openai_compatible", "openai_responses", "anthropic", "gemini"} {
		if _, ok := ParseProvider(value); !ok {
			t.Fatal("canonical provider rejected", value)
		}
	}
}
