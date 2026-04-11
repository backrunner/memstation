package domain

import (
	"bytes"
	"encoding/json"
	"strings"
)

// StoredToolResultPayload wraps tool metadata and output into a stable JSON object
// so persisted messages can round-trip arbitrary tool outputs.
func StoredToolResultPayload(items []ToolResult) any {
	if len(items) == 0 {
		return map[string]any{}
	}
	payload := map[string]any{
		"tool_call_id": items[0].ToolCallID,
		"name":         items[0].Name,
	}
	if items[0].Output != nil {
		payload["output"] = items[0].Output
	}
	return payload
}

// DecodeStoredToolResult restores a persisted tool result wrapper. It also tolerates
// older raw-output payloads by treating the whole JSON value as output when no wrapper fields exist.
func DecodeStoredToolResult(raw []byte) (ToolResult, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ToolResult{}, false
	}

	var parsed any
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return ToolResult{}, false
	}

	switch value := parsed.(type) {
	case map[string]any:
		if len(value) == 0 {
			return ToolResult{}, false
		}
		if hasStoredToolResultFields(value) {
			result := ToolResult{
				ToolCallID: stringJSONValue(value["tool_call_id"]),
				Name:       stringJSONValue(value["name"]),
			}
			if output, ok := value["output"]; ok {
				result.Output = output
			}
			if result.ToolCallID != "" || result.Name != "" || result.Output != nil {
				return result, true
			}
			return ToolResult{}, false
		}
		return ToolResult{Output: value}, true
	case []any, string, float64, bool:
		return ToolResult{Output: value}, true
	default:
		return ToolResult{}, false
	}
}

func hasStoredToolResultFields(value map[string]any) bool {
	_, hasCallID := value["tool_call_id"]
	_, hasName := value["name"]
	_, hasOutput := value["output"]
	return hasCallID || hasName || hasOutput
}

func stringJSONValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
