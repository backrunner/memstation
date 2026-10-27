package contract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
	anthropicprovider "memstation/apps/server/internal/provider/anthropic"
	geminiprovider "memstation/apps/server/internal/provider/gemini"
	openaiprovider "memstation/apps/server/internal/provider/openai"
)

func TestNativeMultimodalOutboundWireContracts(t *testing.T) {
	for _, name := range []domain.Provider{domain.ProviderOpenAICompatible, domain.ProviderOpenAIResponses, domain.ProviderAnthropic, domain.ProviderGemini} {
		t.Run(string(name), func(t *testing.T) {
			var captured map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				switch name {
				case domain.ProviderOpenAICompatible:
					_, _ = w.Write([]byte(`{"id":"r","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
				case domain.ProviderOpenAIResponses:
					_, _ = w.Write([]byte(`{"id":"r","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
				case domain.ProviderAnthropic:
					_, _ = w.Write([]byte(`{"id":"r","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
				case domain.ProviderGemini:
					_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
				}
			}))
			defer server.Close()
			resolver := provider.StaticConfigResolver{name: {APIKey: "fixture-key", BaseURL: server.URL}}
			var adapter ports.ProviderAdapter
			switch name {
			case domain.ProviderOpenAICompatible:
				adapter = openaiprovider.NewCompatibleAdapter(resolver, time.Second)
			case domain.ProviderOpenAIResponses:
				adapter = openaiprovider.NewResponsesAdapter(resolver, time.Second)
			case domain.ProviderAnthropic:
				adapter = anthropicprovider.NewAdapter(resolver, time.Second)
			case domain.ProviderGemini:
				adapter = geminiprovider.NewAdapter(resolver, time.Second)
			}
			_, err := adapter.Complete(context.Background(), domain.UnifiedRequest{Provider: name, Model: "fixture", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Blocks: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "look"}, {Type: domain.ContentBlockImage, SourceURL: "data:image/png;base64,aGk="}, {Type: domain.ContentBlockAudio, SourceURL: "data:audio/wav;base64,aGk="}, {Type: domain.ContentBlockFile, SourceURL: "data:application/pdf;base64,aGk=", Filename: "a.pdf"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(captured)
			wire := string(raw)
			var expected []string
			switch name {
			case domain.ProviderOpenAICompatible:
				expected = []string{`"type":"text"`, `"type":"image_url"`, `"image_url":{"url":"data:image/png;base64,aGk="}`, `"input_audio":{"data":"aGk=","format":"wav"}`, `"file":{"file_data":"data:application/pdf;base64,aGk=","filename":"a.pdf"}`}
			case domain.ProviderOpenAIResponses:
				expected = []string{`"type":"input_text"`, `"type":"input_image"`, `"image_url":"data:image/png;base64,aGk="`, `"type":"input_file"`, `"file_data":"data:application/pdf;base64,aGk="`}
			case domain.ProviderAnthropic:
				expected = []string{`"type":"image"`, `"source":{"data":"aGk=","media_type":"image/png","type":"base64"}`, `"type":"document"`, `audio omitted`}
			case domain.ProviderGemini:
				expected = []string{`"inlineData":{"data":"aGk=","mimeType":"image/png"}`, `"inlineData":{"data":"aGk=","mimeType":"audio/wav"}`, `"inlineData":{"data":"aGk=","mimeType":"application/pdf"}`}
			}
			for _, want := range expected {
				if !strings.Contains(wire, want) {
					t.Fatal("missing native wire field", want, wire)
				}
			}
		})
	}
}
