package protocol

import (
	"encoding/json"
	"fmt"
	"strings"

	"memstation/apps/server/internal/domain"
)

type OpenAIToolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type AnthropicToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func normalizeOpenAITools(items []OpenAIToolSpec) []domain.ToolDefinition {
	if len(items) == 0 {
		return nil
	}
	out := make([]domain.ToolDefinition, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Function.Name) == "" {
			continue
		}
		out = append(out, domain.ToolDefinition{
			Name:        item.Function.Name,
			Description: item.Function.Description,
			InputSchema: item.Function.Parameters,
			ReadOnly:    false,
		})
	}
	return out
}

func normalizeAnthropicTools(items []AnthropicToolSpec) []domain.ToolDefinition {
	if len(items) == 0 {
		return nil
	}
	out := make([]domain.ToolDefinition, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		out = append(out, domain.ToolDefinition{
			Name:        item.Name,
			Description: item.Description,
			InputSchema: item.InputSchema,
			ReadOnly:    false,
		})
	}
	return out
}

func normalizeOpenAIToolCalls(items []OpenAIChatToolCall) []domain.ToolCall {
	if len(items) == 0 {
		return nil
	}
	out := make([]domain.ToolCall, 0, len(items))
	for _, item := range items {
		out = append(out, domain.ToolCall{
			ID:        item.ID,
			Name:      item.Function.Name,
			Arguments: parseArguments(item.Function.Arguments),
		})
	}
	return out
}

func parseArguments(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		var nested any
		if json.Unmarshal([]byte(asString), &nested) == nil {
			return nested
		}
		return asString
	}
	return string(raw)
}

func formatOpenAIToolCalls(items []domain.ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"id":   item.ID,
			"type": "function",
			"function": map[string]any{
				"name":      item.Name,
				"arguments": marshalArguments(item.Arguments),
			},
		})
	}
	return out
}

func marshalArguments(arguments any) string {
	if arguments == nil {
		return "{}"
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func toolResultText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		return contentText(typed)
	case map[string]any:
		if content, ok := typed["content"]; ok {
			return toolResultText(content)
		}
		raw, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(raw)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

func formatResponsesToolCalls(items []domain.ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"id":        item.ID,
			"type":      "function_call",
			"call_id":   item.ID,
			"name":      item.Name,
			"arguments": marshalArguments(item.Arguments),
		})
	}
	return out
}

func anyString(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", typed)
	}
}
