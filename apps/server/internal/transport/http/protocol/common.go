package protocol

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
)

type HeaderOptions struct {
	ConversationID   string
	Mode             domain.Mode
	Provider         domain.Provider
	EndConversation  bool
	InjectContext    bool
	InjectTools      bool
	ToolWriteEnabled bool
	MemoryPersist    bool
	SourceRetain     *bool
}

func ParseHeaderOptions(r *http.Request) HeaderOptions {
	memoryPersist := parseBooleanHeader(r.Header.Get("X-MemStation-Memory-Persist"), true)
	return HeaderOptions{
		ConversationID:   r.Header.Get("X-MemStation-Session"),
		Mode:             parseMode(r.Header.Get("X-MemStation-Mode")),
		Provider:         domain.Provider(strings.ToLower(r.Header.Get("X-MemStation-Provider"))),
		EndConversation:  strings.EqualFold(r.Header.Get("X-MemStation-End-Conversation"), "true"),
		InjectContext:    strings.EqualFold(r.Header.Get("X-MemStation-Inject-Context"), "true"),
		InjectTools:      strings.EqualFold(r.Header.Get("X-MemStation-Inject-Tools"), "true"),
		ToolWriteEnabled: memoryPersist && strings.EqualFold(r.Header.Get("X-MemStation-Tool-Write"), "true"),
		MemoryPersist:    memoryPersist,
		SourceRetain:     parseOptionalBooleanHeader(r.Header.Get("X-MemStation-Source-Retain")),
	}
}

func parseMode(value string) domain.Mode {
	if strings.EqualFold(value, string(domain.ModeInject)) {
		return domain.ModeInject
	}
	return domain.ModeTransparent
}

func toBlocks(raw json.RawMessage) []domain.ContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []domain.ContentBlock{{Type: domain.ContentBlockText, Text: text}}
	}
	return []domain.ContentBlock{{Type: domain.ContentBlockJSON, Raw: string(raw)}}
}

func textFromBlocks(raw json.RawMessage) string {
	blocks := toBlocks(raw)
	if len(blocks) == 0 {
		return ""
	}
	if blocks[0].Text != "" {
		return blocks[0].Text
	}
	return blocks[0].Raw
}

func buildRequest(protocol domain.Protocol, model string, stream bool, headers HeaderOptions, messages []domain.UnifiedMessage, tools []domain.ToolDefinition) domain.UnifiedRequest {
	conversationID := strings.TrimSpace(headers.ConversationID)
	if conversationID == "" {
		conversationID = uuid.NewString()
	}
	return domain.UnifiedRequest{
		RequestID:        uuid.NewString(),
		ConversationID:   conversationID,
		SessionID:        conversationID,
		Protocol:         protocol,
		Provider:         headers.Provider,
		Mode:             headers.Mode,
		Model:            model,
		Stream:           stream,
		EndConversation:  headers.EndConversation,
		InjectContext:    headers.InjectContext,
		InjectTools:      headers.InjectTools,
		ToolWriteEnabled: headers.ToolWriteEnabled,
		MemoryPersist:    boolPtr(headers.MemoryPersist),
		SourceRetain:     headers.SourceRetain,
		Messages:         messages,
		Tools:            tools,
		ReceivedAt:       time.Now().UTC(),
	}
}

func parseBooleanHeader(value string, fallback bool) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return fallback
	}
	switch trimmed {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func parseOptionalBooleanHeader(value string) *bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return nil
	}
	parsed := parseBooleanHeader(trimmed, false)
	return &parsed
}
