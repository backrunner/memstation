package protocol

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

func TestChatCompletionStreamWriterClosesWithToolCalls(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writer, err := NewChatCompletionStreamWriter(recorder)
	if err != nil {
		t.Fatalf("new stream writer: %v", err)
	}

	if err := writer.Close(streamToolCallResponse(domain.ProtocolOpenAIChat)); err != nil {
		t.Fatalf("close stream: %v", err)
	}

	body := recorder.Body.String()
	for _, expected := range []string{`"tool_calls"`, `"name":"recall_memory"`, `"arguments":"{\"query\":\"alpha\"}"`, `"finish_reason":"tool_calls"`, "data: [DONE]"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected body to contain %q, got %s", expected, body)
		}
	}
}

func TestResponsesStreamWriterClosesWithToolCalls(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writer, err := NewResponsesStreamWriter(recorder)
	if err != nil {
		t.Fatalf("new stream writer: %v", err)
	}

	if err := writer.Close(streamToolCallResponse(domain.ProtocolOpenAIResponses)); err != nil {
		t.Fatalf("close stream: %v", err)
	}

	body := recorder.Body.String()
	for _, expected := range []string{`"type":"response.output_item.done"`, `"type":"function_call"`, `"name":"recall_memory"`, `"arguments":"{\"query\":\"alpha\"}"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected body to contain %q, got %s", expected, body)
		}
	}
}

func TestAnthropicStreamWriterClosesWithToolCalls(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writer, err := NewAnthropicStreamWriter(recorder)
	if err != nil {
		t.Fatalf("new stream writer: %v", err)
	}

	if err := writer.Close(streamToolCallResponse(domain.ProtocolAnthropic)); err != nil {
		t.Fatalf("close stream: %v", err)
	}

	body := recorder.Body.String()
	for _, expected := range []string{`event: content_block_start`, `"type":"tool_use"`, `"name":"recall_memory"`, `"query":"alpha"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected body to contain %q, got %s", expected, body)
		}
	}
	if strings.Contains(body, `"content_block":{"type":"text","text":""}`) {
		t.Fatalf("expected pure tool response not to open an empty text block, got %s", body)
	}
	for _, expected := range []string{`"index":0`, `"id":"call-1"`, `"type":"tool_use"`, `"stop_reason":"tool_use"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected body to contain %q for tool-use close, got %s", expected, body)
		}
	}
}

func TestAnthropicStreamWriterDoesNotOpenTextBlockOnStartOnlyToolCall(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	writer, err := NewAnthropicStreamWriter(recorder)
	if err != nil {
		t.Fatalf("new stream writer: %v", err)
	}

	if err := writer.Event(domain.StreamEvent{
		Type:       domain.StreamEventResponseStart,
		ResponseID: "resp-tool-stream",
		Model:      "claude-3-5-sonnet",
		Role:       domain.RoleAssistant,
	}); err != nil {
		t.Fatalf("start stream: %v", err)
	}
	if err := writer.Close(streamToolCallResponse(domain.ProtocolAnthropic)); err != nil {
		t.Fatalf("close stream: %v", err)
	}

	body := recorder.Body.String()
	if strings.Contains(body, `"content_block":{"type":"text","text":""}`) {
		t.Fatalf("expected start-only pure tool response not to open an empty text block, got %s", body)
	}
	if !strings.Contains(body, `"index":0`) || !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Fatalf("expected tool block at index 0 and tool_use stop reason, got %s", body)
	}
}

func streamToolCallResponse(protocol domain.Protocol) domain.UnifiedResponse {
	return domain.UnifiedResponse{
		ResponseID: "resp-tool-stream",
		Protocol:   protocol,
		Provider:   domain.ProviderOpenAICompatible,
		Model:      "gpt-4.1-mini",
		Message: domain.UnifiedMessage{
			Role: domain.RoleAssistant,
			ToolCalls: []domain.ToolCall{{
				ID:        "call-1",
				Name:      "recall_memory",
				Arguments: map[string]any{"query": "alpha"},
			}},
		},
		Usage:     domain.UsageRecord{InputTokens: 10, OutputTokens: 3, TotalTokens: 13},
		CreatedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}
}
