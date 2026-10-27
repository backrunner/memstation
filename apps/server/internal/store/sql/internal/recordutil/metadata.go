package recordutil

import (
	"encoding/json"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

func CloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func Key(parts ...string) string {
	encoded, _ := json.Marshal(parts)
	return string(encoded)
}

func StringMetadataValue(metadata map[string]any, field string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[field]
	if !ok || value == nil {
		return ""
	}
	typed, ok := value.(string)
	if !ok {
		return ""
	}
	return typed
}

func BoolMetadataValue(metadata map[string]any, field string) bool {
	if metadata == nil {
		return false
	}
	value, ok := metadata[field]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func StringSliceMetadataValue(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func RecordBefore(stamp time.Time, id string, position *domain.RecordPosition) bool {
	return position == nil || stamp.Before(position.UpdatedAt) || (stamp.Equal(position.UpdatedAt) && id < position.ID)
}
