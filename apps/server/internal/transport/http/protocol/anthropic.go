package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"memstation/apps/server/internal/domain"
)

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicMessagesRequest struct {
	Model    string              `json:"model"`
	Stream   bool                `json:"stream"`
	Messages []AnthropicMessage  `json:"messages"`
	Tools    []AnthropicToolSpec `json:"tools"`
}

func NormalizeAnthropic(r AnthropicMessagesRequest, headers HeaderOptions) domain.UnifiedRequest {
	messages := make([]domain.UnifiedMessage, 0, len(r.Messages))
	for _, message := range r.Messages {
		text, toolCalls, toolResults := anthropicContent(message.Content)
		unified := domain.UnifiedMessage{
			Role:        domain.MessageRole(message.Role),
			Text:        text,
			Blocks:      toBlocks(message.Content),
			ToolCalls:   toolCalls,
			ToolResults: toolResults,
		}
		if len(toolResults) > 0 && unified.Role == domain.RoleUser {
			unified.Role = domain.RoleTool
		}
		messages = append(messages, unified)
	}
	return buildRequest(domain.ProtocolAnthropic, r.Model, r.Stream, headers, messages, normalizeAnthropicTools(r.Tools))
}

func FormatAnthropic(resp domain.UnifiedResponse) map[string]any {
	content := make([]map[string]any, 0, 1+len(resp.Message.ToolCalls))
	if resp.Message.Text != "" || len(resp.Message.ToolCalls) == 0 {
		content = append(content, map[string]any{
			"type": "text",
			"text": resp.Message.Text,
		})
	}
	for _, call := range resp.Message.ToolCalls {
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Name,
			"input": call.Arguments,
		})
	}
	stopReason := "end_turn"
	if len(resp.Message.ToolCalls) > 0 {
		stopReason = "tool_use"
	}
	return map[string]any{
		"id":          resp.ResponseID,
		"type":        "message",
		"role":        "assistant",
		"model":       resp.Model,
		"stop_reason": stopReason,
		"content":     content,
		"usage": map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
		},
	}
}

func WriteAnthropicStream(w http.ResponseWriter, resp domain.UnifiedResponse) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	messages := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"" + resp.ResponseID + "\",\"model\":\"" + resp.Model + "\",\"role\":\"assistant\"}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":" + quoteJSON(resp.Message.Text) + "}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	for _, msg := range messages {
		if _, err := w.Write([]byte(msg)); err != nil {
			return err
		}
		flusher.Flush()
	}
	return nil
}

func quoteJSON(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func anthropicContent(raw json.RawMessage) (string, []domain.ToolCall, []domain.ToolResult) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil, nil
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return textFromBlocks(raw), nil, nil
	}
	parts := make([]string, 0, len(items))
	toolCalls := make([]domain.ToolCall, 0)
	toolResults := make([]domain.ToolResult, 0)
	for _, item := range items {
		switch anyString(item["type"]) {
		case "text":
			if value := anyString(item["text"]); value != "" {
				parts = append(parts, value)
			}
		case "tool_use":
			toolCalls = append(toolCalls, domain.ToolCall{
				ID:        anyString(item["id"]),
				Name:      anyString(item["name"]),
				Arguments: mapValue(item["input"]),
			})
		case "tool_result":
			toolResults = append(toolResults, domain.ToolResult{
				ToolCallID: anyString(item["tool_use_id"]),
				Output: map[string]any{
					"content": anyString(item["content"]),
				},
			})
		}
	}
	return strings.Join(parts, "\n"), toolCalls, toolResults
}

func mapValue(value any) map[string]any {
	typed, _ := value.(map[string]any)
	return typed
}
