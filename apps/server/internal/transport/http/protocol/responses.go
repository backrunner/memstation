package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

type OpenAIResponsesRequest struct {
	Model  string           `json:"model"`
	Stream bool             `json:"stream"`
	Input  json.RawMessage  `json:"input"`
	Tools  []OpenAIToolSpec `json:"tools"`
}

func NormalizeResponses(r OpenAIResponsesRequest, headers HeaderOptions) domain.UnifiedRequest {
	messages := normalizeResponsesInput(r.Input)
	if len(messages) == 0 {
		messages = []domain.UnifiedMessage{{Role: domain.RoleUser, Text: textFromBlocks(r.Input), Blocks: toBlocks(r.Input)}}
	}
	return buildRequest(domain.ProtocolOpenAIResponses, r.Model, r.Stream, headers, messages, normalizeOpenAITools(r.Tools))
}

func FormatResponses(resp domain.UnifiedResponse) map[string]any {
	output := make([]map[string]any, 0, 1+len(resp.Message.ToolCalls))
	if len(resp.Message.ToolCalls) > 0 {
		output = append(output, formatResponsesToolCalls(resp.Message.ToolCalls)...)
	}
	if resp.Message.Text != "" || len(output) == 0 {
		output = append(output, map[string]any{
			"id":   resp.ResponseID + "_msg",
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": resp.Message.Text,
			}},
		})
	}
	return map[string]any{
		"id":         resp.ResponseID,
		"object":     "response",
		"created_at": resp.CreatedAt.Format(time.RFC3339),
		"status":     "completed",
		"model":      resp.Model,
		"output":     output,
		"usage": map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		},
	}
}

func WriteResponsesStream(w http.ResponseWriter, resp domain.UnifiedResponse) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	events := []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": resp.ResponseID, "model": resp.Model}},
		{"type": "response.output_text.delta", "delta": resp.Message.Text},
		{"type": "response.completed", "response": map[string]any{"id": resp.ResponseID, "status": "completed"}},
	}
	encoder := json.NewEncoder(w)
	for _, event := range events {
		if _, err := w.Write([]byte("data: ")); err != nil {
			return err
		}
		if err := encoder.Encode(event); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return err
		}
		flusher.Flush()
	}
	return nil
}

func normalizeResponsesInput(raw json.RawMessage) []domain.UnifiedMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]domain.UnifiedMessage, 0, len(items))
	for _, item := range items {
		switch anyString(item["type"]) {
		case "function_call":
			out = append(out, domain.UnifiedMessage{
				Role: domain.RoleAssistant,
				ToolCalls: []domain.ToolCall{{
					ID:        firstNonEmpty(anyString(item["call_id"]), anyString(item["id"])),
					Name:      anyString(item["name"]),
					Arguments: parseArguments(rawJSON(item["arguments"])),
				}},
			})
		case "function_call_output":
			out = append(out, domain.UnifiedMessage{
				Role: domain.RoleTool,
				Text: anyString(item["output"]),
				ToolResults: []domain.ToolResult{{
					ToolCallID: anyString(item["call_id"]),
					Output:     toolResultOutputMap(anyString(item["output"])),
				}},
			})
		case "message":
			out = append(out, domain.UnifiedMessage{
				Role:   domain.MessageRole(anyString(item["role"])),
				Text:   contentText(item["content"]),
				Blocks: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: contentText(item["content"])}},
			})
		}
	}
	return out
}

func contentText(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text := anyString(block["text"]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func rawJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return raw
}
