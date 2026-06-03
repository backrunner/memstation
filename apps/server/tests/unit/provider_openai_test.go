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
	openaiprovider "memstation/apps/server/internal/provider/openai"
)

func TestOpenAIAdapterComplete(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["model"] != "gpt-4.1-mini" {
			t.Fatalf("unexpected model payload: %v", payload["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"created": time.Now().Unix(),
			"model":   "gpt-4.1-mini",
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": "hello from upstream",
				},
			}},
			"usage": map[string]any{
				"prompt_tokens":     11,
				"completion_tokens": 7,
				"total_tokens":      18,
			},
		})
	}))
	defer server.Close()

	adapter := openaiprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderOpenAI: {
			APIKey:  "sk-test",
			BaseURL: server.URL,
		},
	}, 5*time.Second)
	response, err := adapter.Complete(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIChat,
		Model:    "gpt-4.1-mini",
		Messages: []domain.UnifiedMessage{{
			Role: domain.RoleUser,
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if response.Provider != domain.ProviderOpenAI {
		t.Fatalf("unexpected provider: %s", response.Provider)
	}
	if response.Message.Text != "hello from upstream" {
		t.Fatalf("unexpected response text: %s", response.Message.Text)
	}
	if response.Usage.TotalTokens != 18 {
		t.Fatalf("unexpected usage: %+v", response.Usage)
	}
}

func TestOpenAIAdapterStreamChatCompletions(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/chat/completions" {
			t.Fatalf("unexpected path: %s", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_stream","created":1710000000,"model":"gpt-4.1-mini","choices":[{"delta":{"role":"assistant","content":"hello "}}]}`),
			fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_stream","created":1710000000,"model":"gpt-4.1-mini","choices":[{"delta":{"content":"world"}}]}`),
			fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_stream","created":1710000000,"model":"gpt-4.1-mini","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`),
			"data: [DONE]\n\n",
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

	adapter := openaiprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderOpenAI: {
			APIKey:  "sk-test",
			BaseURL: server.URL,
		},
	}, 5*time.Second)

	events := make([]domain.StreamEvent, 0, 3)
	response, err := adapter.Stream(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIChat,
		Model:    "gpt-4.1-mini",
		Stream:   true,
		Messages: []domain.UnifiedMessage{{
			Role: domain.RoleUser,
			Text: "hello",
		}},
	}, func(event domain.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if response.Message.Text != "hello world" {
		t.Fatalf("unexpected streamed text: %s", response.Message.Text)
	}
	if response.Usage.TotalTokens != 20 {
		t.Fatalf("unexpected usage: %+v", response.Usage)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 stream events, got %d", len(events))
	}
	if events[0].Type != domain.StreamEventResponseStart {
		t.Fatalf("expected first event to be start, got %s", events[0].Type)
	}
	if events[1].Delta != "hello " || events[2].Delta != "world" {
		t.Fatalf("unexpected delta events: %+v", events)
	}
	if events[3].Type != domain.StreamEventUsage {
		t.Fatalf("expected usage event, got %s", events[3].Type)
	}
}

func TestOpenAIAdapterCompleteParsesToolCalls(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_tool",
			"created": time.Now().Unix(),
			"model":   "gpt-4.1-mini",
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []map[string]any{{
						"id":   "call-1",
						"type": "function",
						"function": map[string]any{
							"name":      "recall_memory",
							"arguments": `{"query":"alpha"}`,
						},
					}},
				},
			}},
			"usage": map[string]any{
				"prompt_tokens":     11,
				"completion_tokens": 4,
				"total_tokens":      15,
			},
		})
	}))
	defer server.Close()

	adapter := openaiprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderOpenAI: {APIKey: "sk-test", BaseURL: server.URL},
	}, 5*time.Second)

	response, err := adapter.Complete(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIChat,
		Model:    "gpt-4.1-mini",
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
		t.Fatalf("unexpected tool name: %+v", response.Message.ToolCalls[0])
	}
}

func TestOpenAIAdapterStreamChatCompletionsParsesToolCalls(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_stream_tool","created":1710000000,"model":"gpt-4.1-mini","choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"recall_memory","arguments":""}}]}}]}`),
			fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_stream_tool","created":1710000000,"model":"gpt-4.1-mini","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"alpha\"}"}}]}}]}`),
			fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_stream_tool","created":1710000000,"model":"gpt-4.1-mini","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16}}`),
			"data: [DONE]\n\n",
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

	adapter := openaiprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderOpenAI: {APIKey: "sk-test", BaseURL: server.URL},
	}, 5*time.Second)

	response, err := adapter.Stream(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIChat,
		Model:    "gpt-4.1-mini",
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
	if call.ID != "call-1" || call.Name != "recall_memory" {
		t.Fatalf("unexpected tool call: %+v", call)
	}
	arguments, ok := call.Arguments.(map[string]any)
	if !ok || arguments["query"] != "alpha" {
		t.Fatalf("unexpected tool arguments: %+v", call.Arguments)
	}
}

func TestOpenAIResponsesAdapterStreamParsesToolCalls(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			fmt.Sprintf("data: %s\n\n", `{"type":"response.created","response":{"id":"resp_stream_tool","model":"gpt-4.1-mini"}}`),
			fmt.Sprintf("data: %s\n\n", `{"type":"response.output_item.added","output_index":0,"item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"recall_memory","arguments":""}}`),
			fmt.Sprintf("data: %s\n\n", `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"query\":\"alpha\"}"}`),
			fmt.Sprintf("data: %s\n\n", `{"type":"response.completed","response":{"id":"resp_stream_tool","model":"gpt-4.1-mini","usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}}}`),
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

	adapter := openaiprovider.NewResponsesAdapter(provider.StaticConfigResolver{
		domain.ProviderOpenAIResponses: {APIKey: "sk-test", BaseURL: server.URL},
	}, 5*time.Second)

	response, err := adapter.Stream(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIResponses,
		Model:    "gpt-4.1-mini",
		Stream:   true,
		Tools:    []domain.ToolDefinition{{Name: "recall_memory"}},
		Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "hello"}},
	}, func(domain.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected streamed tool call, got %+v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "recall_memory" {
		t.Fatalf("unexpected tool call: %+v", call)
	}
	arguments, ok := call.Arguments.(map[string]any)
	if !ok || arguments["query"] != "alpha" {
		t.Fatalf("unexpected tool arguments: %+v", call.Arguments)
	}
}

func TestOpenAIResponsesAdapterStreamParsesToolCallArgumentDoneByItemID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			fmt.Sprintf("data: %s\n\n", `{"type":"response.created","response":{"id":"resp_stream_tool_done","model":"gpt-4.1-mini"}}`),
			fmt.Sprintf("data: %s\n\n", `{"type":"response.output_item.added","output_index":0,"item":{"id":"fc-1","type":"function_call","call_id":"call-1","name":"recall_memory","arguments":""}}`),
			fmt.Sprintf("data: %s\n\n", `{"type":"response.function_call_arguments.done","item_id":"fc-1","arguments":"{\"query\":\"alpha\",\"limit\":3}"}`),
			fmt.Sprintf("data: %s\n\n", `{"type":"response.completed","response":{"id":"resp_stream_tool_done","model":"gpt-4.1-mini","usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}}}`),
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

	adapter := openaiprovider.NewResponsesAdapter(provider.StaticConfigResolver{
		domain.ProviderOpenAIResponses: {APIKey: "sk-test", BaseURL: server.URL},
	}, 5*time.Second)

	response, err := adapter.Stream(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIResponses,
		Model:    "gpt-4.1-mini",
		Stream:   true,
		Tools:    []domain.ToolDefinition{{Name: "recall_memory"}},
		Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "hello"}},
	}, func(domain.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected streamed tool call, got %+v", response.Message.ToolCalls)
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "recall_memory" {
		t.Fatalf("unexpected tool call: %+v", call)
	}
	arguments, ok := call.Arguments.(map[string]any)
	if !ok || arguments["query"] != "alpha" || arguments["limit"] != float64(3) {
		t.Fatalf("unexpected tool arguments: %+v", call.Arguments)
	}
}

func TestOpenAIResponsesAdapterStreamIgnoresMessageOutputItemsForToolCalls(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			fmt.Sprintf("data: %s\n\n", `{"type":"response.created","response":{"id":"resp_stream_text","model":"gpt-4.1-mini"}}`),
			fmt.Sprintf("data: %s\n\n", `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg-1","type":"message"}}`),
			fmt.Sprintf("data: %s\n\n", `{"type":"response.output_text.delta","delta":"hello"}`),
			fmt.Sprintf("data: %s\n\n", `{"type":"response.completed","response":{"id":"resp_stream_text","model":"gpt-4.1-mini","output":[{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}`),
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

	adapter := openaiprovider.NewResponsesAdapter(provider.StaticConfigResolver{
		domain.ProviderOpenAIResponses: {APIKey: "sk-test", BaseURL: server.URL},
	}, 5*time.Second)

	response, err := adapter.Stream(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIResponses,
		Model:    "gpt-4.1-mini",
		Stream:   true,
		Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "hello"}},
	}, func(domain.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("stream responses: %v", err)
	}
	if response.Message.Text != "hello" {
		t.Fatalf("expected streamed text, got %q", response.Message.Text)
	}
	if len(response.Message.ToolCalls) != 0 {
		t.Fatalf("expected no tool calls for message output item, got %+v", response.Message.ToolCalls)
	}
}

func TestOpenAIAdapterCompleteRetriesTransientStatus(t *testing.T) {
	t.Parallel()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "temporary upstream failure", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_retry",
			"created": time.Now().Unix(),
			"model":   "gpt-4.1-mini",
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": "retry success",
				},
			}},
			"usage": map[string]any{
				"prompt_tokens":     12,
				"completion_tokens": 6,
				"total_tokens":      18,
			},
		})
	}))
	defer server.Close()

	adapter := openaiprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderOpenAI: {
			APIKey:  "sk-test",
			BaseURL: server.URL,
		},
	}, 5*time.Second)

	response, err := adapter.Complete(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIChat,
		Model:    "gpt-4.1-mini",
		Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("complete with retry: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 upstream attempts, got %d", attempts)
	}
	if response.Message.Text != "retry success" {
		t.Fatalf("unexpected response text: %s", response.Message.Text)
	}
}

func TestOpenAIAdapterStreamRetriesTransientStatus(t *testing.T) {
	t.Parallel()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "temporary upstream failure", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []string{
			fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_stream_retry","created":1710000000,"model":"gpt-4.1-mini","choices":[{"delta":{"role":"assistant","content":"retry "}}]}`),
			fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_stream_retry","created":1710000000,"model":"gpt-4.1-mini","choices":[{"delta":{"content":"stream"}}]}`),
			fmt.Sprintf("data: %s\n\n", `{"id":"chatcmpl_stream_retry","created":1710000000,"model":"gpt-4.1-mini","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`),
			"data: [DONE]\n\n",
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

	adapter := openaiprovider.NewAdapter(provider.StaticConfigResolver{
		domain.ProviderOpenAI: {
			APIKey:  "sk-test",
			BaseURL: server.URL,
		},
	}, 5*time.Second)

	response, err := adapter.Stream(context.Background(), domain.UnifiedRequest{
		Protocol: domain.ProtocolOpenAIChat,
		Model:    "gpt-4.1-mini",
		Stream:   true,
		Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "hello"}},
	}, func(domain.StreamEvent) error { return nil })
	if err != nil {
		t.Fatalf("stream with retry: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 upstream attempts, got %d", attempts)
	}
	if response.Message.Text != "retry stream" {
		t.Fatalf("unexpected streamed text: %s", response.Message.Text)
	}
}
