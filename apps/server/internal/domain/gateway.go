package domain

import (
	"strings"
	"time"
)

type Protocol string

type Provider string

type Mode string

type MessageRole string

type ContentBlockType string

type MemoryType string

type StreamEventType string

const (
	ProtocolOpenAIChat      Protocol = "openai_chat_completions"
	ProtocolOpenAIResponses Protocol = "openai_responses"
	ProtocolAnthropic       Protocol = "anthropic_messages"
)

const (
	ProviderOpenAICompatible Provider = "openai_compatible"
	ProviderOpenAI           Provider = ProviderOpenAICompatible
	ProviderOpenAIResponses  Provider = "openai_responses"
	ProviderAnthropic        Provider = "anthropic"
	ProviderGemini           Provider = "gemini"
)

const (
	ModeTransparent Mode = "transparent"
	ModeInject      Mode = "inject"
)

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

const (
	ContentBlockText       ContentBlockType = "text"
	ContentBlockImage      ContentBlockType = "image"
	ContentBlockAudio      ContentBlockType = "audio"
	ContentBlockVideo      ContentBlockType = "video"
	ContentBlockFile       ContentBlockType = "file"
	ContentBlockToolUse    ContentBlockType = "tool_use"
	ContentBlockToolResult ContentBlockType = "tool_result"
	ContentBlockJSON       ContentBlockType = "json"
	ContentBlockUnknown    ContentBlockType = "unknown"
)

const (
	MemoryTypeSummary    MemoryType = "summary"
	MemoryTypeSegment    MemoryType = "segment"
	MemoryTypeImpression MemoryType = "impression"
	MemoryTypeFact       MemoryType = "fact"
	MemoryTypeKeyMemory  MemoryType = "key_memory"
)

const (
	StreamEventResponseStart StreamEventType = "response.start"
	StreamEventTextDelta     StreamEventType = "text.delta"
	StreamEventUsage         StreamEventType = "usage"
)

type ContentBlock struct {
	Type           ContentBlockType `json:"type"`
	MIMEType       string           `json:"mime_type,omitempty"`
	BlobID         string           `json:"blob_id,omitempty"`
	SourceURL      string           `json:"source_url,omitempty"`
	ProviderFileID string           `json:"provider_file_id,omitempty"`
	Filename       string           `json:"filename,omitempty"`
	SizeBytes      int64            `json:"size_bytes,omitempty"`
	SHA256         string           `json:"sha256,omitempty"`
	PageIndex      int              `json:"page_index,omitempty"`
	StartMS        int64            `json:"start_ms,omitempty"`
	EndMS          int64            `json:"end_ms,omitempty"`
	DurationMS     int64            `json:"duration_ms,omitempty"`
	Text           string           `json:"text,omitempty"`
	Raw            string           `json:"raw,omitempty"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	ReadOnly    bool           `json:"read_only"`
}

type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ToolResult struct {
	ToolCallID string         `json:"tool_call_id"`
	Name       string         `json:"name"`
	Output     map[string]any `json:"output,omitempty"`
}

type UsageRecord struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type UnifiedMessage struct {
	Role        MessageRole    `json:"role"`
	Text        string         `json:"text,omitempty"`
	Blocks      []ContentBlock `json:"blocks,omitempty"`
	ToolCalls   []ToolCall     `json:"tool_calls,omitempty"`
	ToolResults []ToolResult   `json:"tool_results,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type UnifiedRequest struct {
	RequestID          string           `json:"request_id"`
	ConversationID     string           `json:"conversation_id,omitempty"`
	SessionID          string           `json:"session_id,omitempty"`
	UserID             string           `json:"user_id,omitempty"`
	Protocol           Protocol         `json:"protocol"`
	Provider           Provider         `json:"provider"`
	Mode               Mode             `json:"mode"`
	Model              string           `json:"model,omitempty"`
	Stream             bool             `json:"stream"`
	EndConversation    bool             `json:"end_conversation"`
	InjectContext      bool             `json:"inject_context"`
	InjectTools        bool             `json:"inject_tools"`
	ToolWriteEnabled   bool             `json:"tool_write_enabled"`
	MaxInjectionTokens int              `json:"max_injection_tokens,omitempty"`
	MemoryPersist      *bool            `json:"memory_persist,omitempty"`
	SourceRetain       *bool            `json:"source_retain,omitempty"`
	Messages           []UnifiedMessage `json:"messages,omitempty"`
	Tools              []ToolDefinition `json:"tools,omitempty"`
	ProviderMetadata   map[string]any   `json:"provider_metadata,omitempty"`
	RequestMetadata    map[string]any   `json:"request_metadata,omitempty"`
	ReceivedAt         time.Time        `json:"received_at"`
}

func (r UnifiedRequest) PersistMemoryEnabled() bool {
	return r.MemoryPersist == nil || *r.MemoryPersist
}

func (r UnifiedRequest) SourceRetainEnabled(defaultValue bool) bool {
	if r.SourceRetain == nil {
		return defaultValue
	}
	return *r.SourceRetain
}

type UnifiedResponse struct {
	ResponseID string         `json:"response_id"`
	Protocol   Protocol       `json:"protocol"`
	Provider   Provider       `json:"provider"`
	Model      string         `json:"model,omitempty"`
	Message    UnifiedMessage `json:"message"`
	Usage      UsageRecord    `json:"usage"`
	CreatedAt  time.Time      `json:"created_at"`
	Raw        map[string]any `json:"raw,omitempty"`
}

type StreamEvent struct {
	Type       StreamEventType `json:"type"`
	ResponseID string          `json:"response_id,omitempty"`
	Provider   Provider        `json:"provider,omitempty"`
	Model      string          `json:"model,omitempty"`
	Role       MessageRole     `json:"role,omitempty"`
	Delta      string          `json:"delta,omitempty"`
	Usage      UsageRecord     `json:"usage,omitempty"`
	CreatedAt  time.Time       `json:"created_at,omitempty"`
	Raw        map[string]any  `json:"raw,omitempty"`
}

type ModelCapability struct {
	ID             string   `json:"id"`
	Provider       Provider `json:"provider"`
	Role           string   `json:"role"`
	SupportsTools  bool     `json:"supports_tools"`
	SupportsStream bool     `json:"supports_stream"`
}

func NormalizeProvider(value string) Provider {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "", "default":
		return ""
	case "openai", "openai_compatible", "openai_compat", "chat_completions":
		return ProviderOpenAICompatible
	case "openai_responses", "responses", "response":
		return ProviderOpenAIResponses
	case "anthropic", "anthropic_messages", "messages":
		return ProviderAnthropic
	case "gemini", "google", "google_genai":
		return ProviderGemini
	default:
		return Provider(normalized)
	}
}

func ParseProvider(value string) (Provider, bool) {
	provider := NormalizeProvider(value)
	switch provider {
	case ProviderOpenAICompatible, ProviderOpenAIResponses, ProviderAnthropic, ProviderGemini:
		return provider, true
	default:
		return "", false
	}
}

func LLMProviders() []Provider {
	return []Provider{
		ProviderOpenAICompatible,
		ProviderOpenAIResponses,
		ProviderAnthropic,
		ProviderGemini,
	}
}

func EmbeddingProviders() []Provider {
	return []Provider{
		ProviderOpenAICompatible,
	}
}

func (b ContentBlock) TextProxy() string {
	if strings.TrimSpace(b.Text) != "" {
		return strings.TrimSpace(b.Text)
	}
	switch b.Type {
	case ContentBlockToolUse, ContentBlockToolResult, ContentBlockJSON, ContentBlockUnknown:
		return strings.TrimSpace(b.Raw)
	default:
		if strings.TrimSpace(b.Raw) != "" {
			return strings.TrimSpace(b.Raw)
		}
		if b.Metadata != nil {
			for _, key := range []string{"text", "ocr_text", "caption", "transcript", "summary", "excerpt"} {
				if value, ok := b.Metadata[key].(string); ok && strings.TrimSpace(value) != "" {
					return strings.TrimSpace(value)
				}
			}
		}
		return ""
	}
}

func (b ContentBlock) HasSource() bool {
	return strings.TrimSpace(b.BlobID) != "" || strings.TrimSpace(b.SourceURL) != "" || strings.TrimSpace(b.ProviderFileID) != ""
}

func (b ContentBlock) Modality() string {
	switch b.Type {
	case ContentBlockImage, ContentBlockAudio, ContentBlockVideo, ContentBlockFile:
		return string(b.Type)
	case ContentBlockText, ContentBlockJSON:
		return "text"
	default:
		return string(b.Type)
	}
}
