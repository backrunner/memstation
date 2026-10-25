package openai

import (
	"context"
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
