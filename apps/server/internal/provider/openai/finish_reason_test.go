package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
)

func TestChatPreservesTruncationWithoutExposingReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"test","choices":[{"finish_reason":"length","message":{"role":"assistant","content":"","reasoning_content":"private reasoning"}}],"usage":{"prompt_tokens":12,"completion_tokens":8192,"total_tokens":8204}}`)
	}))
	defer server.Close()
	adapter := NewCompatibleAdapter(provider.StaticConfigResolver{domain.ProviderOpenAICompatible: {BaseURL: server.URL, APIKey: "fixture"}}, time.Second)
	r, e := adapter.Complete(context.Background(), domain.UnifiedRequest{Protocol: domain.ProtocolOpenAIChat, Model: "fixture", MaxOutputTokens: 8192})
	if e != nil {
		t.Fatal(e)
	}
	if r.FinishReason != "length" || r.Message.Text != "" || r.Usage.OutputTokens != 8192 {
		t.Fatal(r)
	}
}

func TestExplicitReasoningEffortUsesProtocolField(t *testing.T) {
	for _, protocol := range []domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolOpenAIResponses} {
		for _, effort := range []string{"", "none", "low"} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				got := body["reasoning_effort"]
				if protocol == domain.ProtocolOpenAIResponses {
					got = nil
					if _, ok := body["reasoning_effort"]; ok {
						t.Error("Responses used the Chat reasoning field")
					}
					if reasoning, ok := body["reasoning"].(map[string]any); ok {
						got = reasoning["effort"]
					}
				}
				if (effort == "" && got != nil) || (effort != "" && got != effort) {
					t.Errorf("reasoning setting changed: %s %v", effort, got)
				}
				if protocol == domain.ProtocolOpenAIChat {
					fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{}"}}]}`)
				} else {
					fmt.Fprint(w, `{"id":"r","output":[]}`)
				}
			}))
			adapter := NewCompatibleAdapter(provider.StaticConfigResolver{domain.ProviderOpenAICompatible: {BaseURL: server.URL, APIKey: "fixture"}}, time.Second)
			if protocol == domain.ProtocolOpenAIResponses {
				adapter = NewResponsesAdapter(provider.StaticConfigResolver{domain.ProviderOpenAIResponses: {BaseURL: server.URL, APIKey: "fixture"}}, time.Second)
			}
			_, err := adapter.Complete(context.Background(), domain.UnifiedRequest{Protocol: protocol, Model: "configured", ReasoningEffort: effort})
			server.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
