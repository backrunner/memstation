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

func (a *Adapter) Name() domain.Provider { return domain.ProviderAnthropic }

func (a *Adapter) Complete(ctx context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
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
		ID      string `json:"id"`
		Model   string `json:"model"`
		Role    string `json:"role"`
		Content []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
		Usage struct {
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

	parts := make([]string, 0, len(response.Content))
	toolCalls := make([]domain.ToolCall, 0)
	for _, item := range response.Content {
		switch item.Type {
		case "text":
			if item.Text != "" {
				parts = append(parts, item.Text)
			}
		case "tool_use":
			toolCalls = append(toolCalls, domain.ToolCall{
				ID:        item.ID,
				Name:      item.Name,
				Arguments: item.Input,
			})
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
			Blocks: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: text,
			}},
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

func (a *Adapter) Stream(ctx context.Context, req domain.UnifiedRequest, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
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
		case "content_block_delta":
			var chunk struct {
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return err
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
			"provider": "anthropic",
		},
	}, nil
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
				content = append(content, map[string]any{
					"type":        "tool_result",
					"tool_use_id": result.ToolCallID,
					"content":     toolResultContent(result),
				})
			}
			out = append(out, map[string]any{
				"role":    "user",
				"content": content,
			})
			continue
		}

		content := make([]map[string]any, 0, len(message.ToolCalls)+1)
		if text := provider.MessageText(message); text != "" {
			content = append(content, map[string]any{
				"type": "text",
				"text": text,
			})
		}
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
	if value, ok := result.Output["content"].(string); ok && strings.TrimSpace(value) != "" {
		return value
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
