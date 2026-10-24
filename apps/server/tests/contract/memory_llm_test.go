package contract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
	anthropic "memstation/apps/server/internal/provider/anthropic"
	gemini "memstation/apps/server/internal/provider/gemini"
	openai "memstation/apps/server/internal/provider/openai"
)

func TestMemoryLLMOutboundControls(t *testing.T) {
	for _, tc := range []struct {
		name     string
		factory  func(provider.ConfigResolver, time.Duration) ports.ProviderAdapter
		response string
		check    func(*testing.T, map[string]any)
	}{
		{"chat", func(r provider.ConfigResolver, d time.Duration) ports.ProviderAdapter {
			return openai.NewCompatibleAdapter(r, d)
		}, `{"choices":[{"message":{"role":"assistant","content":"{}"}}]}`, func(t *testing.T, p map[string]any) {
			if p["max_tokens"] != float64(8192) || p["response_format"].(map[string]any)["type"] != "json_object" {
				t.Fatal(p)
			}
		}},
		{"responses", func(r provider.ConfigResolver, d time.Duration) ports.ProviderAdapter {
			return openai.NewResponsesAdapter(r, d)
		}, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{}"}]}]}`, func(t *testing.T, p map[string]any) {
			if p["max_output_tokens"] != float64(8192) || p["text"].(map[string]any)["format"].(map[string]any)["type"] != "json_object" {
				t.Fatal(p)
			}
		}},
		{"anthropic", func(r provider.ConfigResolver, d time.Duration) ports.ProviderAdapter {
			return anthropic.NewAdapter(r, d)
		}, `{"role":"assistant","content":[{"type":"text","text":"{}"}]}`, func(t *testing.T, p map[string]any) {
			if p["max_tokens"] != float64(8192) {
				t.Fatal(p)
			}
		}},
		{"gemini", func(r provider.ConfigResolver, d time.Duration) ports.ProviderAdapter { return gemini.NewAdapter(r, d) }, `{"candidates":[{"content":{"role":"model","parts":[{"text":"{}"}]}}]}`, func(t *testing.T, p map[string]any) {
			c := p["generationConfig"].(map[string]any)
			if c["maxOutputTokens"] != float64(8192) || c["responseMimeType"] != "application/json" {
				t.Fatal(p)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			defer upstream.Close()
			resolver := provider.StaticConfigResolver{}
			for _, name := range []domain.Provider{domain.ProviderOpenAICompatible, domain.ProviderOpenAIResponses, domain.ProviderAnthropic, domain.ProviderGemini} {
				resolver[name] = provider.RuntimeConfig{BaseURL: upstream.URL, APIKey: "test"}
			}
			adapter := tc.factory(resolver, time.Second)
			response, err := adapter.Complete(context.Background(), domain.UnifiedRequest{Model: "configured-memory-model", Protocol: domain.ProtocolOpenAIChat, MaxOutputTokens: 8192, JSONOutput: true, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Return JSON"}}})
			if err != nil {
				t.Fatal(err)
			}
			if response.Message.Text != "{}" {
				t.Fatal(response)
			}
			tc.check(t, payload)
		})
	}
}
