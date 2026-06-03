package unit_test

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
	anthropicprovider "memstation/apps/server/internal/provider/anthropic"
)

func TestAnthropicAdapterStreamMessages(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/messages" {
			t.Fatalf("unexpected path: %s", got)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-ant-test" {
			t.Fatalf("unexpected api key header: %s", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			fmt.Sprintf("event: message_start\ndata: %s\n\n", `{"type":"message_start","message":{"id":"msg_stream","model":"claude-3-7-sonnet","role":"assistant","usage":{"input_tokens":13,"output_tokens":0}}}`),
			fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello "}}`),
			fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"anthropic"}}`),
			fmt.Sprintf("event: message_delta\ndata: %s\n\n", `{"type":"message_delta","usage":{"output_tokens":9}}`),
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, event := range events {
			if _, err := w.Write([]byte(event)); err != nil {
				t.Fatalf("write event: %v", err)
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	adapter := anthropicprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderAnthropic: {
			APIKey:  "sk-ant-test",
			BaseURL: server.URL,
		},
	}, 5*time.Second)

	streamEvents := make([]domain.StreamEvent, 0, 4)
	response, err := adapter.Stream(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolAnthropic,
		Model:    "claude-3-7-sonnet",
		Stream:   true,
		Messages: []domain.UnifiedMessage{{
			Role: domain.RoleUser,
			Text: "hello",
		}},
	}, func(event domain.StreamEvent) error {
		streamEvents = append(streamEvents, event)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if response.Message.Text != "hello anthropic" {
		t.Fatalf("unexpected streamed text: %s", response.Message.Text)
	}
	if response.Usage.InputTokens != 13 || response.Usage.OutputTokens != 9 {
		t.Fatalf("unexpected usage: %+v", response.Usage)
	}
	if len(streamEvents) < 4 {
		t.Fatalf("expected multiple stream events, got %d", len(streamEvents))
	}
	if streamEvents[0].Type != domain.StreamEventResponseStart {
		t.Fatalf("expected first event to be start, got %s", streamEvents[0].Type)
	}
}

func TestAnthropicAdapterCompleteParsesToolUse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "msg_tool",
			"model": "claude-3-7-sonnet",
			"role":  "assistant",
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "toolu_1",
				"name":  "recall_memory",
				"input": map[string]any{"query": "alpha"},
			}},
			"usage": map[string]any{
				"input_tokens":  9,
				"output_tokens": 7,
			},
		})
	}))
	defer server.Close()

	adapter := anthropicprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderAnthropic: {
			APIKey:  "sk-ant-test",
			BaseURL: server.URL,
		},
	}, 5*time.Second)

	response, err := adapter.Complete(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolAnthropic,
		Model:    "claude-3-7-sonnet",
		Tools: []domain.ToolDefinition{{
			Name: "recall_memory",
		}},
		Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected tool call, got %+v", response.Message.ToolCalls)
	}
	if response.Message.ToolCalls[0].Name != "recall_memory" {
		t.Fatalf("unexpected tool call: %+v", response.Message.ToolCalls[0])
	}
}

func TestAnthropicAdapterStreamParsesToolUse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			fmt.Sprintf("event: message_start\ndata: %s\n\n", `{"type":"message_start","message":{"id":"msg_stream_tool","model":"claude-3-7-sonnet","role":"assistant","usage":{"input_tokens":13,"output_tokens":0}}}`),
			fmt.Sprintf("event: content_block_start\ndata: %s\n\n", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"recall_memory","input":{}}}`),
			fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"alpha\"}"}}`),
			fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", `{"type":"content_block_stop","index":0}`),
			fmt.Sprintf("event: message_delta\ndata: %s\n\n", `{"type":"message_delta","usage":{"output_tokens":7}}`),
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, event := range events {
			if _, err := w.Write([]byte(event)); err != nil {
				t.Fatalf("write event: %v", err)
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	adapter := anthropicprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderAnthropic: {APIKey: "sk-ant-test", BaseURL: server.URL},
	}, 5*time.Second)

	response, err := adapter.Stream(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolAnthropic,
		Model:    "claude-3-7-sonnet",
		Stream:   true,
		Tools:    []domain.ToolDefinition{{Name: "recall_memory"}},
		Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "hello"}},
	}, func(domain.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected streamed tool call, got %+v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "toolu_1" || call.Name != "recall_memory" {
		t.Fatalf("unexpected tool call: %+v", call)
	}
	arguments, ok := call.Arguments.(map[string]any)
	if !ok || arguments["query"] != "alpha" {
		t.Fatalf("unexpected tool arguments: %+v", call.Arguments)
	}
}
