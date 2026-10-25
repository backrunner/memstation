package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	"memstation/apps/server/internal/runtimeconfig"
)

type Adapter struct {
	client       *http.Client
	resolver     provider.ConfigResolver
	name         domain.Provider
	useResponses bool
	timeout      *runtimeconfig.DurationValue
}

func NewAdapter(resolver provider.ConfigResolver, timeout time.Duration) *Adapter {
	return NewCompatibleAdapter(resolver, timeout)
}

func NewCompatibleAdapter(resolver provider.ConfigResolver, timeout time.Duration) *Adapter {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Adapter{
		client:   &http.Client{},
		resolver: resolver,
		name:     domain.ProviderOpenAICompatible,
		timeout:  runtimeconfig.NewDurationValue(timeout),
	}
}

func NewResponsesAdapter(resolver provider.ConfigResolver, timeout time.Duration) *Adapter {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Adapter{
		client:       &http.Client{},
		resolver:     resolver,
		name:         domain.ProviderOpenAIResponses,
		useResponses: true,
		timeout:      runtimeconfig.NewDurationValue(timeout),
	}
}

func (a *Adapter) Name() domain.Provider {
	if a == nil || a.name == "" {
		return domain.ProviderOpenAICompatible
	}
	return a.name
}

func (a *Adapter) Complete(ctx context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	ctx, cancel := a.withTimeout(ctx)
	defer cancel()

	config, err := a.resolveConfig(ctx)
	if err != nil {
		return nil, err
	}
	if a.useResponses {
		return a.completeResponses(ctx, req, config)
	}
	return a.completeChat(ctx, req, config)
}

func (a *Adapter) Stream(ctx context.Context, req domain.UnifiedRequest, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	ctx, cancel := a.withTimeout(ctx)
	defer cancel()

	config, err := a.resolveConfig(ctx)
	if err != nil {
		return nil, err
	}
	if a.useResponses {
		return a.streamResponses(ctx, req, config, emit)
	}
	return a.streamChat(ctx, req, config, emit)
}

func (a *Adapter) resolveConfig(ctx context.Context) (provider.RuntimeConfig, error) {
	config, err := a.resolver.Resolve(ctx, a.Name())
	if err != nil {
		return provider.RuntimeConfig{}, err
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return provider.RuntimeConfig{}, fmt.Errorf("%s api key is not configured", a.Name())
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

func (a *Adapter) completeChat(ctx context.Context, req domain.UnifiedRequest, config provider.RuntimeConfig) (*domain.UnifiedResponse, error) {
	payload := map[string]any{
		"model":    req.Model,
		"messages": buildMessages(req.Messages),
		"stream":   false,
	}
	if req.MaxOutputTokens > 0 {
		payload["max_tokens"] = req.MaxOutputTokens
	}
	if req.JSONOutput {
		payload["response_format"] = map[string]any{"type": "json_object"}
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildTools(req.Tools)
	}
	raw, _, err := provider.DoJSONRequest(ctx, a.client, a.Name(), http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/chat/completions", map[string]string{
		"Authorization": "Bearer " + config.APIKey,
	}, payload)
	if err != nil {
		return nil, err
	}

	var response struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Created int64  `json:"created"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role      string `json:"role"`
				Content   any    `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	text := ""
	role := domain.RoleAssistant
	toolCalls := []domain.ToolCall{}
	blocks := []domain.ContentBlock{}
	if len(response.Choices) > 0 {
		blocks = contentToBlocks(response.Choices[0].Message.Content)
		text = blocksText(blocks)
		if text == "" {
			text = contentToText(response.Choices[0].Message.Content)
		}
		if response.Choices[0].Message.Role != "" {
			role = domain.MessageRole(response.Choices[0].Message.Role)
		}
		toolCalls = parseChatToolCalls(response.Choices[0].Message.ToolCalls)
	}
	createdAt := time.Now().UTC()
	if response.Created > 0 {
		createdAt = time.Unix(response.Created, 0).UTC()
	}
	if response.ID == "" {
		response.ID = uuid.NewString()
	}
	unified := newUnifiedResponse(req.Protocol, response.ID, a.Name(), firstNonEmpty(response.Model, req.Model), role, text, createdAt, domain.UsageRecord{
		InputTokens:  response.Usage.PromptTokens,
		OutputTokens: response.Usage.CompletionTokens,
		TotalTokens:  response.Usage.TotalTokens,
	})
	if len(blocks) > 0 {
		unified.Message.Blocks = blocks
	}
	unified.Message.ToolCalls = toolCalls
	if len(response.Choices) > 0 {
		unified.FinishReason = response.Choices[0].FinishReason
	}
	return unified, nil
}

func (a *Adapter) completeResponses(ctx context.Context, req domain.UnifiedRequest, config provider.RuntimeConfig) (*domain.UnifiedResponse, error) {
	payload := map[string]any{
		"model":  req.Model,
		"input":  buildResponsesInput(req.Messages),
		"stream": false,
	}
	if req.MaxOutputTokens > 0 {
		payload["max_output_tokens"] = req.MaxOutputTokens
	}
	if req.JSONOutput {
		payload["text"] = map[string]any{"format": map[string]any{"type": "json_object"}}
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildResponsesTools(req.Tools)
	}
	raw, _, err := provider.DoJSONRequest(ctx, a.client, a.Name(), http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/responses", map[string]string{
		"Authorization": "Bearer " + config.APIKey,
	}, payload)
	if err != nil {
		return nil, err
	}

	var response struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Output []struct {
			ID        string           `json:"id"`
			Type      string           `json:"type"`
			Role      string           `json:"role"`
			Name      string           `json:"name"`
			CallID    string           `json:"call_id"`
			Arguments string           `json:"arguments"`
			Content   []map[string]any `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.ID == "" {
		response.ID = uuid.NewString()
	}
	blocks, text, toolCalls := responseOutput(response.Output)
	unified := newUnifiedResponse(req.Protocol, response.ID, a.Name(), firstNonEmpty(response.Model, req.Model), domain.RoleAssistant, text, time.Now().UTC(), domain.UsageRecord{
		InputTokens:  response.Usage.InputTokens,
		OutputTokens: response.Usage.OutputTokens,
		TotalTokens:  response.Usage.TotalTokens,
	})
	if len(blocks) > 0 {
		unified.Message.Blocks = blocks
	}
	unified.Message.ToolCalls = toolCalls
	return unified, nil
}

func (a *Adapter) streamChat(ctx context.Context, req domain.UnifiedRequest, config provider.RuntimeConfig, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	payload := map[string]any{
		"model":    req.Model,
		"messages": buildMessages(req.Messages),
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildTools(req.Tools)
	}
	response, err := provider.DoStreamRequest(ctx, a.client, a.Name(), http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/chat/completions", map[string]string{
		"Authorization": "Bearer " + config.APIKey,
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
	toolAccumulator := newChatToolCallAccumulator()
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
			Provider:   a.Name(),
			Model:      model,
			Role:       role,
			CreatedAt:  createdAt,
		})
	}

	err = provider.ReadSSE(response.Body, func(event provider.SSEEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" || data == "[DONE]" {
			return nil
		}
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Created int64  `json:"created"`
			Choices []struct {
				Delta struct {
					Role      string `json:"role"`
					Content   any    `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return err
		}
		if chunk.ID != "" {
			responseID = chunk.ID
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Created > 0 {
			createdAt = time.Unix(chunk.Created, 0).UTC()
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Role != "" {
			role = domain.MessageRole(chunk.Choices[0].Delta.Role)
		}
		if err := emitStart(); err != nil {
			return err
		}
		if len(chunk.Choices) > 0 {
			toolAccumulator.Add(chunk.Choices[0].Delta.ToolCalls)
			delta := contentToText(chunk.Choices[0].Delta.Content)
			if delta != "" {
				textBuilder.WriteString(delta)
				if err := emitStreamEvent(emit, domain.StreamEvent{
					Type:       domain.StreamEventTextDelta,
					ResponseID: responseID,
					Provider:   a.Name(),
					Model:      model,
					Role:       role,
					Delta:      delta,
					CreatedAt:  createdAt,
				}); err != nil {
					return err
				}
			}
		}
		if chunk.Usage != nil {
			usage = domain.UsageRecord{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
				TotalTokens:  chunk.Usage.TotalTokens,
			}
			if err := emitStreamEvent(emit, domain.StreamEvent{
				Type:       domain.StreamEventUsage,
				ResponseID: responseID,
				Provider:   a.Name(),
				Model:      model,
				Role:       role,
				Usage:      usage,
				CreatedAt:  createdAt,
			}); err != nil {
				return err
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
	unified := newUnifiedResponse(req.Protocol, responseID, a.Name(), model, role, textBuilder.String(), createdAt, usage)
	unified.Message.ToolCalls = toolAccumulator.ToolCalls()
	return unified, nil
}

func (a *Adapter) streamResponses(ctx context.Context, req domain.UnifiedRequest, config provider.RuntimeConfig, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	payload := map[string]any{
		"model":  req.Model,
		"input":  buildResponsesInput(req.Messages),
		"stream": true,
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildResponsesTools(req.Tools)
	}
	response, err := provider.DoStreamRequest(ctx, a.client, a.Name(), http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/responses", map[string]string{
		"Authorization": "Bearer " + config.APIKey,
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
	responseToolAccumulator := newResponsesToolCallAccumulator()
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
			Provider:   a.Name(),
			Model:      model,
			Role:       role,
			CreatedAt:  createdAt,
		})
	}

	err = provider.ReadSSE(response.Body, func(event provider.SSEEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" || data == "[DONE]" {
			return nil
		}
		var chunk struct {
			Type      string `json:"type"`
			Delta     string `json:"delta"`
			ItemID    string `json:"item_id"`
			Name      string `json:"name"`
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
			Item      struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
			OutputIndex *int `json:"output_index"`
			Response    struct {
				ID     string `json:"id"`
				Model  string `json:"model"`
				Output []struct {
					ID        string           `json:"id"`
					Type      string           `json:"type"`
					Role      string           `json:"role"`
					Name      string           `json:"name"`
					CallID    string           `json:"call_id"`
					Arguments string           `json:"arguments"`
					Content   []map[string]any `json:"content"`
				} `json:"output"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return err
		}
		if chunk.Response.ID != "" {
			responseID = chunk.Response.ID
		}
		if chunk.Response.Model != "" {
			model = chunk.Response.Model
		}
		switch chunk.Type {
		case "response.created":
			return emitStart()
		case "response.output_item.added", "response.output_item.done":
			if chunk.Item.Type == "function_call" {
				responseToolAccumulator.AddItem(outputIndexValue(chunk.OutputIndex), chunk.Item.ID, chunk.Item.CallID, chunk.Item.Name, chunk.Item.Arguments)
			}
			return emitStart()
		case "response.function_call_arguments.delta":
			responseToolAccumulator.AddArgumentsDelta(outputIndexValue(chunk.OutputIndex), chunk.ItemID, chunk.Delta)
			return emitStart()
		case "response.function_call_arguments.done":
			responseToolAccumulator.AddArgumentsDone(outputIndexValue(chunk.OutputIndex), chunk.ItemID, chunk.CallID, chunk.Name, chunk.Arguments)
			return emitStart()
		case "response.output_text.delta":
			if err := emitStart(); err != nil {
				return err
			}
			if chunk.Delta != "" {
				textBuilder.WriteString(chunk.Delta)
				return emitStreamEvent(emit, domain.StreamEvent{
					Type:       domain.StreamEventTextDelta,
					ResponseID: responseID,
					Provider:   a.Name(),
					Model:      model,
					Role:       role,
					Delta:      chunk.Delta,
					CreatedAt:  createdAt,
				})
			}
		case "response.completed":
			if err := emitStart(); err != nil {
				return err
			}
			_, _, completedToolCalls := responseOutput(chunk.Response.Output)
			responseToolAccumulator.AddCompleted(completedToolCalls)
			usage = domain.UsageRecord{
				InputTokens:  chunk.Response.Usage.InputTokens,
				OutputTokens: chunk.Response.Usage.OutputTokens,
				TotalTokens:  chunk.Response.Usage.TotalTokens,
			}
			if usage.TotalTokens > 0 {
				return emitStreamEvent(emit, domain.StreamEvent{
					Type:       domain.StreamEventUsage,
					ResponseID: responseID,
					Provider:   a.Name(),
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
	unified := newUnifiedResponse(req.Protocol, responseID, a.Name(), model, role, textBuilder.String(), createdAt, usage)
	unified.Message.ToolCalls = responseToolAccumulator.ToolCalls()
	return unified, nil
}

func buildMessages(messages []domain.UnifiedMessage) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.Role == domain.RoleTool && len(message.ToolResults) > 0:
			for _, result := range message.ToolResults {
				out = append(out, map[string]any{
					"role":         "tool",
					"tool_call_id": result.ToolCallID,
					"content":      toolResultContent(result),
				})
			}
		case message.Role == domain.RoleAssistant && len(message.ToolCalls) > 0:
			item := map[string]any{
				"role":       message.Role,
				"tool_calls": buildChatToolCalls(message.ToolCalls),
			}
			if content := buildOpenAIChatContent(message); content != nil {
				item["content"] = content
			} else {
				item["content"] = nil
			}
			out = append(out, item)
		default:
			content := buildOpenAIChatContent(message)
			if content == nil {
				content = provider.MessageText(message)
			}
			out = append(out, map[string]any{
				"role":    message.Role,
				"content": content,
			})
		}
	}
	return out
}

func buildResponsesInput(messages []domain.UnifiedMessage) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message.Role == domain.RoleTool && len(message.ToolResults) > 0 {
			for _, result := range message.ToolResults {
				output := result.Output
				if output == nil {
					output = toolResultContent(result)
				}
				out = append(out, map[string]any{
					"type":    "function_call_output",
					"call_id": result.ToolCallID,
					"output":  output,
				})
			}
			continue
		}
		if message.Role == domain.RoleAssistant && len(message.ToolCalls) > 0 {
			for _, call := range message.ToolCalls {
				out = append(out, map[string]any{
					"type":      "function_call",
					"call_id":   call.ID,
					"name":      call.Name,
					"arguments": marshalArguments(call.Arguments),
				})
			}
			if provider.MessageText(message) == "" {
				continue
			}
		}
		content := buildOpenAIResponsesContent(message)
		if len(content) == 0 {
			text := provider.MessageText(message)
			if text == "" {
				continue
			}
			content = []map[string]any{{
				"type": "input_text",
				"text": text,
			}}
		}
		if len(content) == 0 {
			continue
		}
		out = append(out, map[string]any{
			"role":    message.Role,
			"content": content,
		})
	}
	if len(out) == 0 {
		out = append(out, map[string]any{
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": "Continue.",
			}},
		})
	}
	return out
}

func buildTools(tools []domain.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			},
		})
	}
	return out
}

func buildResponsesTools(tools []domain.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.InputSchema,
		})
	}
	return out
}

func responseOutput(output []struct {
	ID        string           `json:"id"`
	Type      string           `json:"type"`
	Role      string           `json:"role"`
	Name      string           `json:"name"`
	CallID    string           `json:"call_id"`
	Arguments string           `json:"arguments"`
	Content   []map[string]any `json:"content"`
},
) ([]domain.ContentBlock, string, []domain.ToolCall) {
	parts := make([]string, 0)
	blocks := make([]domain.ContentBlock, 0)
	toolCalls := make([]domain.ToolCall, 0)
	for _, item := range output {
		if item.Type == "function_call" {
			toolCalls = append(toolCalls, domain.ToolCall{
				ID:        firstNonEmpty(item.CallID, item.ID),
				Name:      item.Name,
				Arguments: parseArguments(item.Arguments),
			})
			continue
		}
		for _, rawBlock := range item.Content {
			block := blockFromMap(rawBlock)
			blocks = append(blocks, block)
			if text := strings.TrimSpace(block.TextProxy()); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return blocks, strings.Join(parts, "\n"), toolCalls
}

func contentToText(content any) string {
	if content == nil {
		return ""
	}
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if block, ok := item.(map[string]any); ok {
				switch {
				case stringValue(block, "text") != "":
					parts = append(parts, stringValue(block, "text"))
				case stringValue(block, "output_text") != "":
					parts = append(parts, stringValue(block, "output_text"))
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		if blocks := contentToBlocks(content); len(blocks) > 0 {
			if text := blocksText(blocks); text != "" {
				return text
			}
		}
		return fmt.Sprintf("%v", typed)
	}
}

func contentToBlocks(content any) []domain.ContentBlock {
	switch typed := content.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []domain.ContentBlock{{
			Type: domain.ContentBlockText,
			Text: typed,
		}}
	case []any:
		return blocksFromAnySlice(typed)
	case []map[string]any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return blocksFromAnySlice(items)
	case map[string]any:
		return []domain.ContentBlock{blockFromMap(typed)}
	default:
		return nil
	}
}

func blocksFromAnySlice(items []any) []domain.ContentBlock {
	blocks := make([]domain.ContentBlock, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				continue
			}
			blocks = append(blocks, domain.ContentBlock{Type: domain.ContentBlockText, Text: typed})
		case map[string]any:
			blocks = append(blocks, blockFromMap(typed))
		}
	}
	return blocks
}

func blockFromMap(item map[string]any) domain.ContentBlock {
	blockType := strings.TrimSpace(strings.ToLower(stringValue(item, "type")))
	switch blockType {
	case "", "text", "input_text", "output_text":
		return domain.ContentBlock{
			Type:     domain.ContentBlockText,
			Text:     firstNonEmpty(stringValue(item, "text"), stringValue(item, "output_text")),
			Raw:      rawJSONText(item),
			Metadata: metadataWithout(item, "type", "text", "output_text"),
		}
	case "input_image", "image", "image_url":
		sourceURL, fileID, mimeType, metadata := imageSourceFields(item)
		return domain.ContentBlock{
			Type:           domain.ContentBlockImage,
			BlobID:         stringValue(item, "blob_id"),
			SourceURL:      sourceURL,
			ProviderFileID: fileID,
			MIMEType:       mimeType,
			Filename:       firstNonEmpty(stringValue(item, "filename"), stringValue(metadata, "filename")),
			Text:           firstNonEmpty(stringValue(item, "text"), stringValue(metadata, "caption"), stringValue(metadata, "ocr_text")),
			Raw:            rawJSONText(item),
			Metadata:       metadata,
		}
	case "input_audio", "audio":
		sourceURL, fileID, mimeType, metadata := mediaSourceFields(item, "audio")
		return domain.ContentBlock{
			Type:           domain.ContentBlockAudio,
			BlobID:         stringValue(item, "blob_id"),
			SourceURL:      sourceURL,
			ProviderFileID: fileID,
			MIMEType:       mimeType,
			Filename:       firstNonEmpty(stringValue(item, "filename"), stringValue(metadata, "filename")),
			Text:           firstNonEmpty(stringValue(item, "text"), stringValue(metadata, "transcript"), stringValue(metadata, "summary")),
			Raw:            rawJSONText(item),
			StartMS:        int64Value(item["start_ms"]),
			EndMS:          int64Value(item["end_ms"]),
			DurationMS:     int64Value(item["duration_ms"]),
			Metadata:       metadata,
		}
	case "input_video", "video":
		sourceURL, fileID, mimeType, metadata := mediaSourceFields(item, "video")
		return domain.ContentBlock{
			Type:           domain.ContentBlockVideo,
			BlobID:         stringValue(item, "blob_id"),
			SourceURL:      sourceURL,
			ProviderFileID: fileID,
			MIMEType:       mimeType,
			Filename:       firstNonEmpty(stringValue(item, "filename"), stringValue(metadata, "filename")),
			Text:           firstNonEmpty(stringValue(item, "text"), stringValue(metadata, "transcript"), stringValue(metadata, "summary"), stringValue(metadata, "keyframe_summary")),
			Raw:            rawJSONText(item),
			StartMS:        int64Value(item["start_ms"]),
			EndMS:          int64Value(item["end_ms"]),
			DurationMS:     int64Value(item["duration_ms"]),
			Metadata:       metadata,
		}
	case "file", "input_file", "document":
		sourceURL, fileID, mimeType, metadata := mediaSourceFields(item, "file")
		return domain.ContentBlock{
			Type:           domain.ContentBlockFile,
			BlobID:         stringValue(item, "blob_id"),
			SourceURL:      sourceURL,
			ProviderFileID: fileID,
			MIMEType:       mimeType,
			Filename:       firstNonEmpty(stringValue(item, "filename"), stringValue(metadata, "filename")),
			Text:           firstNonEmpty(stringValue(item, "text"), stringValue(metadata, "document_text"), stringValue(metadata, "parsed_text")),
			Raw:            rawJSONText(item),
			PageIndex:      intValue(item["page_index"]),
			StartMS:        int64Value(item["start_ms"]),
			EndMS:          int64Value(item["end_ms"]),
			Metadata:       metadata,
		}
	default:
		return domain.ContentBlock{
			Type:     domain.ContentBlockUnknown,
			Text:     stringValue(item, "text"),
			Raw:      rawJSONText(item),
			Metadata: metadataWithout(item, "type", "text"),
		}
	}
}

func blocksText(blocks []domain.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.TextProxy()); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func imageSourceFields(item map[string]any) (string, string, string, map[string]any) {
	metadata := metadataWithout(item, "type", "text", "image_url", "file_id", "mime_type")
	if raw := item["image_url"]; raw != nil {
		switch typed := raw.(type) {
		case string:
			return typed, stringValue(item, "file_id"), stringValue(item, "mime_type"), metadata
		case map[string]any:
			metadata = mergeMetadataMaps(metadata, metadataWithout(typed))
			return firstNonEmpty(stringValue(typed, "url"), stringValue(typed, "image_url")), firstNonEmpty(stringValue(item, "file_id"), stringValue(typed, "file_id")), firstNonEmpty(stringValue(item, "mime_type"), stringValue(typed, "mime_type")), metadata
		}
	}
	return stringValue(item, "source_url"), stringValue(item, "file_id"), stringValue(item, "mime_type"), metadata
}

func mediaSourceFields(item map[string]any, key string) (string, string, string, map[string]any) {
	metadata := metadataWithout(item, "type", "text", key, "mime_type", "file_id", "source_url")
	if raw := item[key]; raw != nil {
		switch typed := raw.(type) {
		case string:
			return typed, stringValue(item, "file_id"), stringValue(item, "mime_type"), metadata
		case map[string]any:
			metadata = mergeMetadataMaps(metadata, metadataWithout(typed))
			return firstNonEmpty(stringValue(typed, "url"), stringValue(typed, "source_url"), stringValue(item, "source_url")), firstNonEmpty(stringValue(typed, "file_id"), stringValue(item, "file_id")), firstNonEmpty(stringValue(typed, "mime_type"), stringValue(item, "mime_type")), metadata
		}
	}
	return stringValue(item, "source_url"), stringValue(item, "file_id"), stringValue(item, "mime_type"), metadata
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

func mergeMetadataMaps(values ...map[string]any) map[string]any {
	merged := map[string]any{}
	for _, value := range values {
		for key, item := range value {
			merged[key] = item
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func rawJSONText(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}

func buildOpenAIChatContent(message domain.UnifiedMessage) any {
	blocks := canonicalBlocks(message)
	if len(blocks) == 0 {
		return nil
	}
	if len(blocks) == 1 && blocks[0].Type == domain.ContentBlockText {
		return blocks[0].TextProxy()
	}
	items := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		if item := openAIContentBlock(block, false); len(item) > 0 {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return provider.MessageText(message)
	}
	return items
}

func buildOpenAIResponsesContent(message domain.UnifiedMessage) []map[string]any {
	blocks := canonicalBlocks(message)
	if len(blocks) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		if item := openAIContentBlock(block, false); len(item) > 0 {
			items = append(items, item)
		}
	}
	return items
}

func canonicalBlocks(message domain.UnifiedMessage) []domain.ContentBlock {
	if len(message.Blocks) > 0 {
		return message.Blocks
	}
	if strings.TrimSpace(message.Text) == "" {
		return nil
	}
	return []domain.ContentBlock{{Type: domain.ContentBlockText, Text: message.Text}}
}

func openAIContentBlock(block domain.ContentBlock, output bool) map[string]any {
	switch block.Type {
	case domain.ContentBlockText:
		itemType := "input_text"
		if output {
			itemType = "output_text"
		}
		return map[string]any{"type": itemType, "text": block.TextProxy()}
	case domain.ContentBlockImage:
		item := map[string]any{"type": "input_image"}
		if block.SourceURL != "" {
			item["image_url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			item["file_id"] = block.ProviderFileID
		}
		if block.BlobID != "" {
			item["blob_id"] = block.BlobID
		}
		if block.MIMEType != "" {
			item["mime_type"] = block.MIMEType
		}
		return item
	case domain.ContentBlockAudio:
		item := map[string]any{"type": "input_audio"}
		audio := map[string]any{}
		if block.SourceURL != "" {
			audio["url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			audio["file_id"] = block.ProviderFileID
		}
		if block.MIMEType != "" {
			audio["mime_type"] = block.MIMEType
		}
		if len(audio) > 0 {
			item["audio"] = audio
		}
		if block.BlobID != "" {
			item["blob_id"] = block.BlobID
		}
		return item
	case domain.ContentBlockVideo:
		item := map[string]any{"type": "input_video"}
		video := map[string]any{}
		if block.SourceURL != "" {
			video["url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			video["file_id"] = block.ProviderFileID
		}
		if block.MIMEType != "" {
			video["mime_type"] = block.MIMEType
		}
		if len(video) > 0 {
			item["video"] = video
		}
		if block.BlobID != "" {
			item["blob_id"] = block.BlobID
		}
		return item
	case domain.ContentBlockFile:
		item := map[string]any{"type": "file"}
		if block.SourceURL != "" {
			item["source_url"] = block.SourceURL
		}
		if block.ProviderFileID != "" {
			item["file_id"] = block.ProviderFileID
		}
		if block.BlobID != "" {
			item["blob_id"] = block.BlobID
		}
		if block.Filename != "" {
			item["filename"] = block.Filename
		}
		if block.MIMEType != "" {
			item["mime_type"] = block.MIMEType
		}
		return item
	default:
		if text := block.TextProxy(); text != "" {
			itemType := "input_text"
			if output {
				itemType = "output_text"
			}
			return map[string]any{"type": itemType, "text": text}
		}
		return nil
	}
}

func parseChatToolCalls(items []struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
},
) []domain.ToolCall {
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

type chatToolCallAccumulator struct {
	items []chatToolCallState
}

type chatToolCallState struct {
	id        string
	name      string
	arguments strings.Builder
}

func newChatToolCallAccumulator() *chatToolCallAccumulator {
	return &chatToolCallAccumulator{}
}

func (a *chatToolCallAccumulator) Add(items []struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
},
) {
	for _, item := range items {
		if item.Index < 0 {
			continue
		}
		for len(a.items) <= item.Index {
			a.items = append(a.items, chatToolCallState{})
		}
		state := &a.items[item.Index]
		if strings.TrimSpace(item.ID) != "" {
			state.id = item.ID
		}
		if strings.TrimSpace(item.Function.Name) != "" {
			state.name = item.Function.Name
		}
		if item.Function.Arguments != "" {
			state.arguments.WriteString(item.Function.Arguments)
		}
	}
}

func (a *chatToolCallAccumulator) ToolCalls() []domain.ToolCall {
	if a == nil || len(a.items) == 0 {
		return nil
	}
	out := make([]domain.ToolCall, 0, len(a.items))
	for idx, item := range a.items {
		if strings.TrimSpace(item.id) == "" && strings.TrimSpace(item.name) == "" && strings.TrimSpace(item.arguments.String()) == "" {
			continue
		}
		id := item.id
		if strings.TrimSpace(id) == "" {
			id = fmt.Sprintf("call-%d", idx+1)
		}
		out = append(out, domain.ToolCall{
			ID:        id,
			Name:      item.name,
			Arguments: parseArguments(item.arguments.String()),
		})
	}
	return out
}

type responsesToolCallAccumulator struct {
	items         map[int]responsesToolCallState
	indexByItemID map[string]int
	completed     []domain.ToolCall
}

type responsesToolCallState struct {
	itemID       string
	call         domain.ToolCall
	arguments    string
	argumentsSet bool
}

func newResponsesToolCallAccumulator() *responsesToolCallAccumulator {
	return &responsesToolCallAccumulator{
		items:         map[int]responsesToolCallState{},
		indexByItemID: map[string]int{},
	}
}

func (a *responsesToolCallAccumulator) AddItem(index int, id, callID, name, arguments string) {
	if a == nil {
		return
	}
	index = a.resolveIndex(index, id, callID)
	if index < 0 {
		return
	}
	current := a.items[index]
	if strings.TrimSpace(id) != "" {
		current.itemID = id
	}
	if strings.TrimSpace(id) != "" || strings.TrimSpace(callID) != "" {
		current.call.ID = firstNonEmpty(callID, id)
	}
	if strings.TrimSpace(name) != "" {
		current.call.Name = name
	}
	if strings.TrimSpace(arguments) != "" {
		current.arguments = arguments
		current.argumentsSet = true
	}
	a.items[index] = current
	a.recordIndex(index, id, callID)
}

func (a *responsesToolCallAccumulator) AddArgumentsDelta(index int, itemID, delta string) {
	if a == nil || delta == "" {
		return
	}
	index = a.resolveIndex(index, itemID)
	if index < 0 {
		return
	}
	current := a.items[index]
	if strings.TrimSpace(itemID) != "" {
		current.itemID = itemID
	}
	current.arguments += delta
	current.argumentsSet = true
	a.items[index] = current
	a.recordIndex(index, itemID)
}

func (a *responsesToolCallAccumulator) AddArgumentsDone(index int, itemID, callID, name, arguments string) {
	if a == nil {
		return
	}
	index = a.resolveIndex(index, itemID, callID)
	if index < 0 {
		return
	}
	current := a.items[index]
	if strings.TrimSpace(itemID) != "" {
		current.itemID = itemID
	}
	if strings.TrimSpace(callID) != "" {
		current.call.ID = callID
	}
	if strings.TrimSpace(name) != "" {
		current.call.Name = name
	}
	if strings.TrimSpace(arguments) != "" {
		current.arguments = arguments
		current.argumentsSet = true
	}
	a.items[index] = current
	a.recordIndex(index, itemID, callID)
}

func (a *responsesToolCallAccumulator) AddCompleted(items []domain.ToolCall) {
	if a == nil || len(items) == 0 {
		return
	}
	a.completed = append(a.completed, items...)
}

func (a *responsesToolCallAccumulator) ToolCalls() []domain.ToolCall {
	if a == nil {
		return nil
	}
	out := make([]domain.ToolCall, 0, len(a.completed)+len(a.items))
	seen := map[string]struct{}{}
	appendCall := func(call domain.ToolCall) {
		if strings.TrimSpace(call.ID) == "" && strings.TrimSpace(call.Name) == "" {
			return
		}
		key := firstNonEmpty(call.ID, call.Name)
		if strings.TrimSpace(key) == "" {
			out = append(out, call)
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, call)
	}
	for _, call := range a.completed {
		appendCall(call)
	}
	indexes := make([]int, 0, len(a.items))
	for index := range a.items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		state := a.items[index]
		call := state.call
		if strings.TrimSpace(call.ID) == "" {
			call.ID = firstNonEmpty(state.itemID, fmt.Sprintf("call-%d", index+1))
		}
		if state.argumentsSet {
			call.Arguments = parseArguments(state.arguments)
		}
		appendCall(call)
	}
	return out
}

func (a *responsesToolCallAccumulator) resolveIndex(index int, ids ...string) int {
	if a == nil {
		return -1
	}
	if index >= 0 {
		return index
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if existing, ok := a.indexByItemID[id]; ok {
			return existing
		}
	}
	for next := 0; ; next++ {
		if _, ok := a.items[next]; !ok {
			return next
		}
	}
}

func (a *responsesToolCallAccumulator) recordIndex(index int, ids ...string) {
	if a == nil || index < 0 {
		return
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		a.indexByItemID[id] = index
	}
}

func outputIndexValue(index *int) int {
	if index == nil {
		return -1
	}
	return *index
}

func buildChatToolCalls(items []domain.ToolCall) []map[string]any {
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

func parseArguments(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var arguments any
	if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
		return raw
	}
	return arguments
}

func newUnifiedResponse(protocol domain.Protocol, responseID string, providerName domain.Provider, model string, role domain.MessageRole, text string, createdAt time.Time, usage domain.UsageRecord) *domain.UnifiedResponse {
	if responseID == "" {
		responseID = uuid.NewString()
	}
	if role == "" {
		role = domain.RoleAssistant
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return &domain.UnifiedResponse{
		ResponseID: responseID,
		Protocol:   protocol,
		Provider:   providerName,
		Model:      model,
		Message: func() domain.UnifiedMessage {
			message := domain.UnifiedMessage{
				Role: role,
				Text: text,
			}
			if strings.TrimSpace(text) != "" {
				message.Blocks = []domain.ContentBlock{{
					Type: domain.ContentBlockText,
					Text: text,
				}}
			}
			return message
		}(),
		Usage:     usage,
		CreatedAt: createdAt,
		Raw: map[string]any{
			"provider": string(providerName),
		},
	}
}

func emitStreamEvent(emit func(domain.StreamEvent) error, event domain.StreamEvent) error {
	if emit == nil {
		return nil
	}
	return emit(event)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
