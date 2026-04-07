package openai

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
)

type Adapter struct {
	client   *http.Client
	resolver provider.ConfigResolver
}

func NewAdapter(resolver provider.ConfigResolver, timeout time.Duration) *Adapter {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Adapter{
		client:   &http.Client{Timeout: timeout},
		resolver: resolver,
	}
}

func (a *Adapter) Name() domain.Provider { return domain.ProviderOpenAI }

func (a *Adapter) Complete(ctx context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	config, err := a.resolveConfig(ctx)
	if err != nil {
		return nil, err
	}
	if req.Protocol == domain.ProtocolOpenAIResponses {
		return a.completeResponses(ctx, req, config)
	}
	return a.completeChat(ctx, req, config)
}

func (a *Adapter) Stream(ctx context.Context, req domain.UnifiedRequest, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	config, err := a.resolveConfig(ctx)
	if err != nil {
		return nil, err
	}
	if req.Protocol == domain.ProtocolOpenAIResponses {
		return a.streamResponses(ctx, req, config, emit)
	}
	return a.streamChat(ctx, req, config, emit)
}

func (a *Adapter) resolveConfig(ctx context.Context) (provider.RuntimeConfig, error) {
	config, err := a.resolver.Resolve(ctx, domain.ProviderOpenAI)
	if err != nil {
		return provider.RuntimeConfig{}, err
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return provider.RuntimeConfig{}, fmt.Errorf("openai api key is not configured")
	}
	return config, nil
}

func (a *Adapter) completeChat(ctx context.Context, req domain.UnifiedRequest, config provider.RuntimeConfig) (*domain.UnifiedResponse, error) {
	payload := map[string]any{
		"model":    req.Model,
		"messages": buildMessages(req.Messages),
		"stream":   false,
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildTools(req.Tools)
	}
	raw, _, err := provider.DoJSONRequest(ctx, a.client, domain.ProviderOpenAI, http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/chat/completions", map[string]string{
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
			Message struct {
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
	if len(response.Choices) > 0 {
		text = contentToText(response.Choices[0].Message.Content)
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
	unified := newUnifiedResponse(req.Protocol, response.ID, domain.ProviderOpenAI, firstNonEmpty(response.Model, req.Model), role, text, createdAt, domain.UsageRecord{
		InputTokens:  response.Usage.PromptTokens,
		OutputTokens: response.Usage.CompletionTokens,
		TotalTokens:  response.Usage.TotalTokens,
	})
	unified.Message.ToolCalls = toolCalls
	return unified, nil
}

func (a *Adapter) completeResponses(ctx context.Context, req domain.UnifiedRequest, config provider.RuntimeConfig) (*domain.UnifiedResponse, error) {
	payload := map[string]any{
		"model":  req.Model,
		"input":  buildResponsesInput(req.Messages),
		"stream": false,
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildResponsesTools(req.Tools)
	}
	raw, _, err := provider.DoJSONRequest(ctx, a.client, domain.ProviderOpenAI, http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/responses", map[string]string{
		"Authorization": "Bearer " + config.APIKey,
	}, payload)
	if err != nil {
		return nil, err
	}

	var response struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Output []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Role      string `json:"role"`
			Name      string `json:"name"`
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
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
	text, toolCalls := responseOutput(response.Output)
	unified := newUnifiedResponse(req.Protocol, response.ID, domain.ProviderOpenAI, firstNonEmpty(response.Model, req.Model), domain.RoleAssistant, text, time.Now().UTC(), domain.UsageRecord{
		InputTokens:  response.Usage.InputTokens,
		OutputTokens: response.Usage.OutputTokens,
		TotalTokens:  response.Usage.TotalTokens,
	})
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
	response, err := provider.DoStreamRequest(ctx, a.client, domain.ProviderOpenAI, http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/chat/completions", map[string]string{
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
			Provider:   domain.ProviderOpenAI,
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
					Role    string `json:"role"`
					Content any    `json:"content"`
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
			delta := contentToText(chunk.Choices[0].Delta.Content)
			if delta != "" {
				textBuilder.WriteString(delta)
				if err := emitStreamEvent(emit, domain.StreamEvent{
					Type:       domain.StreamEventTextDelta,
					ResponseID: responseID,
					Provider:   domain.ProviderOpenAI,
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
				Provider:   domain.ProviderOpenAI,
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
	return newUnifiedResponse(req.Protocol, responseID, domain.ProviderOpenAI, model, role, textBuilder.String(), createdAt, usage), nil
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
	response, err := provider.DoStreamRequest(ctx, a.client, domain.ProviderOpenAI, http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/responses", map[string]string{
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
			Provider:   domain.ProviderOpenAI,
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
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				ID    string `json:"id"`
				Model string `json:"model"`
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
		case "response.output_text.delta":
			if err := emitStart(); err != nil {
				return err
			}
			if chunk.Delta != "" {
				textBuilder.WriteString(chunk.Delta)
				return emitStreamEvent(emit, domain.StreamEvent{
					Type:       domain.StreamEventTextDelta,
					ResponseID: responseID,
					Provider:   domain.ProviderOpenAI,
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
			usage = domain.UsageRecord{
				InputTokens:  chunk.Response.Usage.InputTokens,
				OutputTokens: chunk.Response.Usage.OutputTokens,
				TotalTokens:  chunk.Response.Usage.TotalTokens,
			}
			if usage.TotalTokens > 0 {
				return emitStreamEvent(emit, domain.StreamEvent{
					Type:       domain.StreamEventUsage,
					ResponseID: responseID,
					Provider:   domain.ProviderOpenAI,
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
	return newUnifiedResponse(req.Protocol, responseID, domain.ProviderOpenAI, model, role, textBuilder.String(), createdAt, usage), nil
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
			if text := provider.MessageText(message); text != "" {
				item["content"] = text
			} else {
				item["content"] = nil
			}
			out = append(out, item)
		default:
			out = append(out, map[string]any{
				"role":    message.Role,
				"content": provider.MessageText(message),
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
				out = append(out, map[string]any{
					"type":    "function_call_output",
					"call_id": result.ToolCallID,
					"output":  toolResultContent(result),
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
		text := provider.MessageText(message)
		if text == "" {
			continue
		}
		out = append(out, map[string]any{
			"role": message.Role,
			"content": []map[string]any{{
				"type": "input_text",
				"text": text,
			}},
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
	ID        string `json:"id"`
	Type      string `json:"type"`
	Role      string `json:"role"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
},
) (string, []domain.ToolCall) {
	parts := make([]string, 0)
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
		for _, block := range item.Content {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
	}
	return strings.Join(parts, "\n"), toolCalls
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
		return fmt.Sprintf("%v", typed)
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
	if value, ok := result.Output["content"].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	raw, err := json.Marshal(result.Output)
	if err != nil {
		return `{"ok":false,"error":"tool result serialization failed"}`
	}
	return string(raw)
}

func marshalArguments(arguments map[string]any) string {
	if len(arguments) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func parseArguments(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
		return map[string]any{
			"raw": raw,
		}
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
		Message: domain.UnifiedMessage{
			Role: role,
			Text: text,
			Blocks: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: text,
			}},
		},
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
