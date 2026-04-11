package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

type OpenAIChatMessage struct {
	Role       string               `json:"role"`
	Content    json.RawMessage      `json:"content"`
	ToolCallID string               `json:"tool_call_id"`
	ToolCalls  []OpenAIChatToolCall `json:"tool_calls"`
}

type OpenAIChatCompletionsRequest struct {
	Model    string              `json:"model"`
	Stream   bool                `json:"stream"`
	Messages []OpenAIChatMessage `json:"messages"`
	Tools    []OpenAIToolSpec    `json:"tools"`
}

func NormalizeChatCompletion(r OpenAIChatCompletionsRequest, headers HeaderOptions) domain.UnifiedRequest {
	messages := make([]domain.UnifiedMessage, 0, len(r.Messages))
	toolNames := map[string]string{}
	for _, message := range r.Messages {
		unified := domain.UnifiedMessage{
			Role:   domain.MessageRole(message.Role),
			Text:   textFromBlocks(message.Content),
			Blocks: toBlocks(message.Content),
		}
		if len(message.ToolCalls) > 0 {
			unified.ToolCalls = normalizeOpenAIToolCalls(message.ToolCalls)
			for _, call := range unified.ToolCalls {
				if call.ID != "" && call.Name != "" {
					toolNames[call.ID] = call.Name
				}
			}
		}
		if strings.EqualFold(message.Role, string(domain.RoleTool)) {
			unified.Role = domain.RoleTool
			unified.ToolResults = []domain.ToolResult{{
				ToolCallID: message.ToolCallID,
				Name:       toolNames[message.ToolCallID],
				Output:     rawJSONValue(message.Content),
			}}
		}
		messages = append(messages, unified)
	}
	return buildRequest(domain.ProtocolOpenAIChat, r.Model, r.Stream, headers, messages, normalizeOpenAITools(r.Tools))
}

func FormatChatCompletion(resp domain.UnifiedResponse) map[string]any {
	message := map[string]any{
		"role":    resp.Message.Role,
		"content": chatCompletionContent(resp.Message),
	}
	finishReason := any("stop")
	if len(resp.Message.ToolCalls) > 0 {
		message["content"] = nil
		message["tool_calls"] = formatOpenAIToolCalls(resp.Message.ToolCalls)
		finishReason = "tool_calls"
	}
	return map[string]any{
		"id":      resp.ResponseID,
		"object":  "chat.completion",
		"created": resp.CreatedAt.Unix(),
		"model":   resp.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		},
	}
}

type OpenAIChatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

func WriteChatCompletionStream(w http.ResponseWriter, resp domain.UnifiedResponse) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	chunks := []map[string]any{
		{
			"id":      resp.ResponseID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   resp.Model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{
					"role":    "assistant",
					"content": resp.Message.Text,
				},
				"finish_reason": nil,
			}},
		},
		{
			"id":      resp.ResponseID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   resp.Model,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "stop",
			}},
		},
	}
	encoder := json.NewEncoder(w)
	for _, chunk := range chunks {
		if _, err := w.Write([]byte("data: ")); err != nil {
			return err
		}
		if err := encoder.Encode(chunk); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return err
		}
		flusher.Flush()
	}
	_, err := w.Write([]byte("data: [DONE]\n\n"))
	flusher.Flush()
	return err
}
