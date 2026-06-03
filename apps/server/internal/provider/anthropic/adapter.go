package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/runtimeconfig"
)

type Adapter struct {
	client   *http.Client
	resolver provider.ConfigResolver
	timeout  *runtimeconfig.DurationValue
}

func NewAdapter(resolver provider.ConfigResolver, timeout time.Duration) *Adapter {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Adapter{
		client:   &http.Client{},
		resolver: resolver,
		timeout:  runtimeconfig.NewDurationValue(timeout),
	}
}

func (a *Adapter) Name() domain.Provider { return domain.ProviderAnthropic }

func (a *Adapter) Complete(ctx context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	ctx, cancel := a.withTimeout(ctx)
	defer cancel()

	config, err := a.resolveConfig(ctx)
	if err != nil {
		return nil, err
	}
	systemParts, chatMessages := provider.SplitMessages(req.Messages)
	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": 1024,
		"messages":   buildMessages(chatMessages),
	}
	if systemText := provider.JoinSystem(systemParts); systemText != "" {
		payload["system"] = systemText
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildTools(req.Tools)
	}
	raw, _, err := provider.DoJSONRequest(ctx, a.client, domain.ProviderAnthropic, http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/messages", map[string]string{
		"x-api-key":         config.APIKey,
		"anthropic-version": "2023-06-01",
	}, payload)
	if err != nil {
		return nil, err
	}

	var response struct {
		ID      string           `json:"id"`
		Model   string           `json:"model"`
		Role    string           `json:"role"`
		Content []map[string]any `json:"content"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.ID == "" {
		response.ID = uuid.NewString()
	}

	blocks := make([]domain.ContentBlock, 0, len(response.Content))
	parts := make([]string, 0, len(response.Content))
	toolCalls := make([]domain.ToolCall, 0)
	for _, item := range response.Content {
		switch strings.TrimSpace(stringValue(item, "type")) {
		case "text":
			block := anthropicResponseBlock(item)
			blocks = append(blocks, block)
			if text := strings.TrimSpace(block.TextProxy()); text != "" {
				parts = append(parts, text)
			}
		case "tool_use":
			toolCalls = append(toolCalls, domain.ToolCall{
				ID:        stringValue(item, "id"),
				Name:      stringValue(item, "name"),
				Arguments: item["input"],
			})
		default:
			block := anthropicResponseBlock(item)
			if block.Type == "" {
				continue
			}
			blocks = append(blocks, block)
			if text := strings.TrimSpace(block.TextProxy()); text != "" {
				parts = append(parts, text)
			}
		}
	}
	text := strings.Join(parts, "\n")

	return &domain.UnifiedResponse{
		ResponseID: response.ID,
		Protocol:   req.Protocol,
		Provider:   domain.ProviderAnthropic,
		Model:      firstNonEmpty(response.Model, req.Model),
		Message: domain.UnifiedMessage{
			Role:      firstRole(response.Role),
			Text:      text,
			ToolCalls: toolCalls,
			Blocks:    blocks,
		},
		Usage: domain.UsageRecord{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
			TotalTokens:  response.Usage.InputTokens + response.Usage.OutputTokens,
		},
		CreatedAt: time.Now().UTC(),
		Raw: map[string]any{
			"provider": "anthropic",
		},
	}, nil
}

func anthropicResponseBlock(item map[string]any) domain.ContentBlock {
	switch strings.TrimSpace(stringValue(item, "type")) {
	case "text":
		return domain.ContentBlock{
			Type:     domain.ContentBlockText,
			Text:     stringValue(item, "text"),
			Raw:      rawJSONText(item),
			Metadata: metadataWithout(item, "type", "text"),
		}
	case "image":
		sourceURL := stringValue(item, "source_url")
		if source, ok := item["source"].(map[string]any); ok {
			sourceURL = firstNonEmpty(sourceURL, stringValue(source, "url"))
		}
		return domain.ContentBlock{
			Type:           domain.ContentBlockImage,
			SourceURL:      sourceURL,
			ProviderFileID: stringValue(item, "file_id"),
			MIMEType:       firstNonEmpty(stringValue(item, "mime_type"), stringValue(item, "media_type")),
			Filename:       stringValue(item, "filename"),
			Text:           firstNonEmpty(stringValue(item, "text"), stringValue(metadataWithout(item), "caption"), stringValue(metadataWithout(item), "ocr_text")),
			Raw:            rawJSONText(item),
			Metadata:       metadataWithout(item, "type", "source_url", "file_id", "mime_type", "media_type", "filename", "text"),
		}
	case "document", "file":
		return domain.ContentBlock{
			Type:           domain.ContentBlockFile,
			SourceURL:      stringValue(item, "source_url"),
			ProviderFileID: stringValue(item, "file_id"),
			MIMEType:       firstNonEmpty(stringValue(item, "mime_type"), stringValue(item, "media_type")),
			Filename:       stringValue(item, "filename"),
			Text:           firstNonEmpty(stringValue(item, "text"), stringValue(metadataWithout(item), "document_text"), stringValue(metadataWithout(item), "parsed_text")),
			Raw:            rawJSONText(item),
			PageIndex:      intValue(item["page_index"]),
			Metadata:       metadataWithout(item, "type", "source_url", "file_id", "mime_type", "media_type", "filename", "text", "page_index"),
		}
	case "audio":
		return domain.ContentBlock{
			Type:           domain.ContentBlockAudio,
			SourceURL:      stringValue(item, "source_url"),
			ProviderFileID: stringValue(item, "file_id"),
			MIMEType:       firstNonEmpty(stringValue(item, "mime_type"), stringValue(item, "media_type")),
			Filename:       stringValue(item, "filename"),
			Text:           firstNonEmpty(stringValue(item, "text"), stringValue(metadataWithout(item), "transcript"), stringValue(metadataWithout(item), "summary")),
			Raw:            rawJSONText(item),
			StartMS:        int64Value(item["start_ms"]),
			EndMS:          int64Value(item["end_ms"]),
			Metadata:       metadataWithout(item, "type", "source_url", "file_id", "mime_type", "media_type", "filename", "text", "start_ms", "end_ms"),
		}
	case "video":
		return domain.ContentBlock{
			Type:           domain.ContentBlockVideo,
			SourceURL:      stringValue(item, "source_url"),
			ProviderFileID: stringValue(item, "file_id"),
			MIMEType:       firstNonEmpty(stringValue(item, "mime_type"), stringValue(item, "media_type")),
			Filename:       stringValue(item, "filename"),
			Text:           firstNonEmpty(stringValue(item, "text"), stringValue(metadataWithout(item), "transcript"), stringValue(metadataWithout(item), "summary"), stringValue(metadataWithout(item), "keyframe_summary")),
			Raw:            rawJSONText(item),
			StartMS:        int64Value(item["start_ms"]),
			EndMS:          int64Value(item["end_ms"]),
			Metadata:       metadataWithout(item, "type", "source_url", "file_id", "mime_type", "media_type", "filename", "text", "start_ms", "end_ms"),
		}
	default:
		if strings.TrimSpace(stringValue(item, "text")) == "" && strings.TrimSpace(rawJSONText(item)) == "" {
			return domain.ContentBlock{}
		}
		return domain.ContentBlock{
			Type:     domain.ContentBlockUnknown,
			Text:     stringValue(item, "text"),
			Raw:      rawJSONText(item),
			Metadata: metadataWithout(item, "type", "text"),
		}
	}
}

func (a *Adapter) Stream(ctx context.Context, req domain.UnifiedRequest, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	ctx, cancel := a.withTimeout(ctx)
	defer cancel()

	config, err := a.resolveConfig(ctx)
	if err != nil {
		return nil, err
	}
	systemParts, chatMessages := provider.SplitMessages(req.Messages)
	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": 1024,
		"stream":     true,
		"messages":   buildMessages(chatMessages),
	}
	if systemText := provider.JoinSystem(systemParts); systemText != "" {
		payload["system"] = systemText
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildTools(req.Tools)
	}
	response, err := provider.DoStreamRequest(ctx, a.client, domain.ProviderAnthropic, http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/messages", map[string]string{
		"x-api-key":         config.APIKey,
		"anthropic-version": "2023-06-01",
	}, payload)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	createdAt := time.Now().UTC()
	responseID := ""
	model := req.Model
	role := domain.RoleAssistant
	textBuilder := strings.Builder{}
	usage := domain.UsageRecord{}
	toolAccumulator := newStreamToolUseAccumulator()
	started := false
	emitStart := func() error {
		if started {
			return nil
		}
		started = true
		if responseID == "" {
			responseID = uuid.NewString()
		}
		return emitStreamEvent(emit, domain.StreamEvent{
			Type:       domain.StreamEventResponseStart,
			ResponseID: responseID,
			Provider:   domain.ProviderAnthropic,
			Model:      model,
			Role:       role,
			CreatedAt:  createdAt,
		})
	}

	err = provider.ReadSSE(response.Body, func(event provider.SSEEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" {
			return nil
		}
		switch event.Event {
		case "ping":
			return nil
		case "message_start":
			var chunk struct {
				Message struct {
					ID    string `json:"id"`
					Model string `json:"model"`
					Role  string `json:"role"`
					Usage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return err
			}
			responseID = chunk.Message.ID
			model = firstNonEmpty(chunk.Message.Model, model)
			role = firstRole(chunk.Message.Role)
			usage.InputTokens = chunk.Message.Usage.InputTokens
			usage.OutputTokens = chunk.Message.Usage.OutputTokens
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
			if err := emitStart(); err != nil {
				return err
			}
			if usage.TotalTokens > 0 {
				return emitStreamEvent(emit, domain.StreamEvent{
					Type:       domain.StreamEventUsage,
					ResponseID: responseID,
					Provider:   domain.ProviderAnthropic,
					Model:      model,
					Role:       role,
					Usage:      usage,
					CreatedAt:  createdAt,
				})
			}
			return nil
		case "content_block_start":
			var chunk struct {
				Index        int            `json:"index"`
				ContentBlock map[string]any `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return err
			}
			if stringValue(chunk.ContentBlock, "type") == "tool_use" {
				toolAccumulator.Start(chunk.Index, stringValue(chunk.ContentBlock, "id"), stringValue(chunk.ContentBlock, "name"), chunk.ContentBlock["input"])
				return emitStart()
			}
			return nil
		case "content_block_delta":
			var chunk struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return err
			}
			if chunk.Delta.Type == "input_json_delta" {
				toolAccumulator.AddInputDelta(chunk.Index, chunk.Delta.PartialJSON)
				return emitStart()
			}
			if chunk.Delta.Type != "text_delta" || chunk.Delta.Text == "" {
				return nil
			}
			if err := emitStart(); err != nil {
				return err
			}
			textBuilder.WriteString(chunk.Delta.Text)
			return emitStreamEvent(emit, domain.StreamEvent{
				Type:       domain.StreamEventTextDelta,
				ResponseID: responseID,
				Provider:   domain.ProviderAnthropic,
				Model:      model,
				Role:       role,
				Delta:      chunk.Delta.Text,
				CreatedAt:  createdAt,
			})
		case "message_delta":
			var chunk struct {
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return err
			}
			usage.OutputTokens = chunk.Usage.OutputTokens
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
			if usage.TotalTokens > 0 {
				if err := emitStart(); err != nil {
					return err
				}
				return emitStreamEvent(emit, domain.StreamEvent{
					Type:       domain.StreamEventUsage,
					ResponseID: responseID,
					Provider:   domain.ProviderAnthropic,
					Model:      model,
					Role:       role,
					Usage:      usage,
					CreatedAt:  createdAt,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !started {
		if err := emitStart(); err != nil {
			return nil, err
		}
	}
	text := textBuilder.String()
	return &domain.UnifiedResponse{
		ResponseID: responseID,
		Protocol:   req.Protocol,
		Provider:   domain.ProviderAnthropic,
		Model:      model,
		Message: domain.UnifiedMessage{
			Role:      role,
			Text:      text,
			ToolCalls: toolAccumulator.ToolCalls(),
			Blocks: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: text,
			}},
		},
		Usage:     usage,
		CreatedAt: createdAt,
		Raw: map[string]any{
			"provider": "anthropic",
		},
	}, nil
}

type streamToolUseAccumulator struct {
	items []streamToolUseState
}

type streamToolUseState struct {
	id       string
	name     string
	input    any
	inputRaw strings.Builder
}

func newStreamToolUseAccumulator() *streamToolUseAccumulator {
	return &streamToolUseAccumulator{}
}

func (a *streamToolUseAccumulator) Start(index int, id, name string, input any) {
	if a == nil || index < 0 {
		return
	}
	for len(a.items) <= index {
		a.items = append(a.items, streamToolUseState{})
	}
	state := &a.items[index]
	if strings.TrimSpace(id) != "" {
		state.id = id
	}
	if strings.TrimSpace(name) != "" {
		state.name = name
	}
	if input != nil {
		state.input = input
	}
}

func (a *streamToolUseAccumulator) AddInputDelta(index int, partialJSON string) {
	if a == nil || index < 0 || partialJSON == "" {
		return
	}
	for len(a.items) <= index {
		a.items = append(a.items, streamToolUseState{})
	}
	a.items[index].inputRaw.WriteString(partialJSON)
}

func (a *streamToolUseAccumulator) ToolCalls() []domain.ToolCall {
	if a == nil || len(a.items) == 0 {
		return nil
	}
	out := make([]domain.ToolCall, 0, len(a.items))
	for index, item := range a.items {
		if strings.TrimSpace(item.id) == "" && strings.TrimSpace(item.name) == "" && strings.TrimSpace(item.inputRaw.String()) == "" && item.input == nil {
			continue
		}
		id := item.id
		if strings.TrimSpace(id) == "" {
			id = fmt.Sprintf("toolu_%d", index+1)
		}
		arguments := item.input
		if raw := strings.TrimSpace(item.inputRaw.String()); raw != "" {
			var parsed any
			if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
				arguments = parsed
			} else {
				arguments = raw
			}
		}
		out = append(out, domain.ToolCall{
			ID:        id,
			Name:      item.name,
			Arguments: arguments,
		})
	}
	return out
}

func (a *Adapter) resolveConfig(ctx context.Context) (provider.RuntimeConfig, error) {
	config, err := a.resolver.Resolve(ctx, domain.ProviderAnthropic)
	if err != nil {
		return provider.RuntimeConfig{}, err
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return provider.RuntimeConfig{}, fmt.Errorf("anthropic api key is not configured")
	}
	return config, nil
}

func (a *Adapter) SetTimeout(timeout time.Duration) {
	if a == nil {
		return
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if a.timeout == nil {
		a.timeout = runtimeconfig.NewDurationValue(timeout)
		return
	}
	a.timeout.Store(timeout)
}

func (a *Adapter) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := 60 * time.Second
	if a != nil && a.timeout != nil {
		if loaded := a.timeout.Load(); loaded > 0 {
			timeout = loaded
		}
	}
	return context.WithTimeout(ctx, timeout)
}

func buildMessages(messages []domain.UnifiedMessage) []map[string]any {
	if len(messages) == 0 {
		return []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": "Continue.",
			}},
		}}
	}
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == domain.RoleTool && len(message.ToolResults) > 0 {
			content := make([]map[string]any, 0, len(message.ToolResults))
			for _, result := range message.ToolResults {
				output := result.Output
				if output == nil {
					output = toolResultContent(result)
				}
				content = append(content, map[string]any{
					"type":        "tool_result",
					"tool_use_id": result.ToolCallID,
					"content":     output,
				})
			}
			out = append(out, map[string]any{
				"role":    "user",
				"content": content,
			})
			continue
		}

		content := anthropicBlocks(message)
		for _, call := range message.ToolCalls {
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    call.ID,
				"name":  call.Name,
				"input": call.Arguments,
			})
		}
		if len(content) == 0 {
			content = append(content, map[string]any{
				"type": "text",
				"text": "Continue.",
			})
		}
		out = append(out, map[string]any{
			"role":    string(message.Role),
			"content": content,
		})
	}
	return out
}

func anthropicBlocks(message domain.UnifiedMessage) []map[string]any {
	blocks := message.Blocks
	if len(blocks) == 0 && strings.TrimSpace(message.Text) != "" {
		blocks = []domain.ContentBlock{{Type: domain.ContentBlockText, Text: message.Text}}
	}
	out := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case domain.ContentBlockText:
			out = append(out, map[string]any{"type": "text", "text": block.TextProxy()})
		case domain.ContentBlockImage:
			item := map[string]any{"type": "image"}
			if block.SourceURL != "" {
				item["source_url"] = block.SourceURL
			}
			if block.ProviderFileID != "" {
				item["file_id"] = block.ProviderFileID
			}
			if block.MIMEType != "" {
				item["mime_type"] = block.MIMEType
			}
			out = append(out, item)
		case domain.ContentBlockFile:
			item := map[string]any{"type": "document"}
			if block.SourceURL != "" {
				item["source_url"] = block.SourceURL
			}
			if block.ProviderFileID != "" {
				item["file_id"] = block.ProviderFileID
			}
			if block.MIMEType != "" {
				item["mime_type"] = block.MIMEType
			}
			if block.Filename != "" {
				item["filename"] = block.Filename
			}
			out = append(out, item)
		case domain.ContentBlockAudio:
			item := map[string]any{"type": "audio"}
			if block.SourceURL != "" {
				item["source_url"] = block.SourceURL
			}
			if block.ProviderFileID != "" {
				item["file_id"] = block.ProviderFileID
			}
			if block.MIMEType != "" {
				item["mime_type"] = block.MIMEType
			}
			out = append(out, item)
		case domain.ContentBlockVideo:
			item := map[string]any{"type": "video"}
			if block.SourceURL != "" {
				item["source_url"] = block.SourceURL
			}
			if block.ProviderFileID != "" {
				item["file_id"] = block.ProviderFileID
			}
			if block.MIMEType != "" {
				item["mime_type"] = block.MIMEType
			}
			out = append(out, item)
		default:
			if text := block.TextProxy(); text != "" {
				out = append(out, map[string]any{"type": "text", "text": text})
			}
		}
	}
	return out
}

func buildTools(tools []domain.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.InputSchema,
		})
	}
	return out
}

func toolResultContent(result domain.ToolResult) string {
	if output, ok := result.Output.(map[string]any); ok {
		if value, ok := output["content"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	raw, err := json.Marshal(result.Output)
	if err != nil {
		return `{"ok":false,"error":"tool result serialization failed"}`
	}
	return string(raw)
}

func emitStreamEvent(emit func(domain.StreamEvent) error, event domain.StreamEvent) error {
	if emit == nil {
		return nil
	}
	return emit(event)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstRole(value string) domain.MessageRole {
	if strings.TrimSpace(value) == "" {
		return domain.RoleAssistant
	}
	return domain.MessageRole(value)
}

func rawJSONText(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}

func metadataWithout(item map[string]any, keys ...string) map[string]any {
	if len(item) == 0 {
		return nil
	}
	excluded := map[string]struct{}{}
	for _, key := range keys {
		excluded[key] = struct{}{}
	}
	metadata := map[string]any{}
	for key, value := range item {
		if _, ok := excluded[key]; ok {
			continue
		}
		metadata[key] = value
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func stringValue(item map[string]any, key string) string {
	value, ok := item[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}
