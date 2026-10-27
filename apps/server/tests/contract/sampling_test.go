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
	"memstation/apps/server/internal/provider/anthropic"
	"memstation/apps/server/internal/provider/gemini"
	"memstation/apps/server/internal/provider/openai"
)

func TestExplicitSamplingTemperatureSurvivesAllOutboundProtocols(t *testing.T) {
	for _, tc := range []struct {
		name     string
		factory  func(provider.ConfigResolver, time.Duration) ports.ProviderAdapter
		complete string
		stream   string
	}{
		{"chat", func(r provider.ConfigResolver, d time.Duration) ports.ProviderAdapter {
			return openai.NewCompatibleAdapter(r, d)
		}, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`, "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"responses", func(r provider.ConfigResolver, d time.Duration) ports.ProviderAdapter {
			return openai.NewResponsesAdapter(r, d)
		}, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"x\",\"output\":[]}}\n\n"},
		{"anthropic", func(r provider.ConfigResolver, d time.Duration) ports.ProviderAdapter {
			return anthropic.NewAdapter(r, d)
		}, `{"role":"assistant","content":[{"type":"text","text":"ok"}]}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"x\",\"role\":\"assistant\",\"model\":\"fixture\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"gemini", func(r provider.ConfigResolver, d time.Duration) ports.ProviderAdapter { return gemini.NewAdapter(r, d) }, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"},
	} {
		for _, stream := range []bool{false, true} {
			for _, temperature := range []*float64{nil, new(float64), samplingValue(0.35)} {
				t.Run(tc.name, func(t *testing.T) {
					var got map[string]any
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
							t.Error(err)
						}
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = w.Write([]byte(tc.stream))
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = w.Write([]byte(tc.complete))
						}
					}))
					defer server.Close()
					resolver := provider.StaticConfigResolver{}
					for _, name := range []domain.Provider{domain.ProviderOpenAICompatible, domain.ProviderOpenAIResponses, domain.ProviderAnthropic, domain.ProviderGemini} {
						resolver[name] = provider.RuntimeConfig{BaseURL: server.URL, APIKey: "fixture"}
					}
					adapter := tc.factory(resolver, time.Second)
					req := domain.UnifiedRequest{Model: "fixture", Temperature: temperature, MaxOutputTokens: 64, JSONOutput: true}
					var err error
					if stream {
						_, err = adapter.Stream(context.Background(), req, nil)
					} else {
						_, err = adapter.Complete(context.Background(), req)
					}
					if err != nil {
						t.Fatal(err)
					}
					if tc.name == "gemini" {
						got = got["generationConfig"].(map[string]any)
					}
					value, exists := got["temperature"]
					if temperature == nil && exists || temperature != nil && (!exists || value != *temperature) {
						t.Fatal("sampling omitted, defaulted or overwritten", stream, value, exists)
					}
				})
			}
		}
	}
}

func samplingValue(value float64) *float64 { return &value }
