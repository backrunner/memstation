package domain

import "time"

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
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderGemini    Provider = "gemini"
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
	ContentBlockText ContentBlockType = "text"
	ContentBlockJSON ContentBlockType = "json"
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
	Type     ContentBlockType `json:"type"`
	Text     string           `json:"text,omitempty"`
	Raw      string           `json:"raw,omitempty"`
	Metadata map[string]any   `json:"metadata,omitempty"`
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
	Messages           []UnifiedMessage `json:"messages,omitempty"`
	Tools              []ToolDefinition `json:"tools,omitempty"`
	ProviderMetadata   map[string]any   `json:"provider_metadata,omitempty"`
	RequestMetadata    map[string]any   `json:"request_metadata,omitempty"`
	ReceivedAt         time.Time        `json:"received_at"`
}

func (r UnifiedRequest) PersistMemoryEnabled() bool {
	return r.MemoryPersist == nil || *r.MemoryPersist
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
