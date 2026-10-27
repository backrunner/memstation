package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

func generationConfig(req domain.UnifiedRequest) map[string]any {
	config := map[string]any{}
	if req.MaxOutputTokens > 0 {
		config["maxOutputTokens"] = req.MaxOutputTokens
	}
	if req.JSONOutput {
		config["responseMimeType"] = "application/json"
	}
	if req.Temperature != nil {
		config["temperature"] = *req.Temperature
	}
	return config
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

func (a *Adapter) Name() domain.Provider { return domain.ProviderGemini }

func (a *Adapter) Complete(ctx context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	ctx, cancel := a.withTimeout(ctx)
	defer cancel()

	config, err := a.resolveConfig(ctx)
	if err != nil {
		return nil, err
	}
	systemParts, chatMessages := provider.SplitMessages(req.Messages)
	payload := map[string]any{
		"contents": buildContents(chatMessages),
	}
	if config := generationConfig(req); len(config) > 0 {
		payload["generationConfig"] = config
	}
	if systemText := provider.JoinSystem(systemParts); systemText != "" {
		payload["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": systemText}},
		}
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildTools(req.Tools)
	}
	endpoint := fmt.Sprintf("%s/models/%s:generateContent?key=%s", strings.TrimRight(config.BaseURL, "/"), url.PathEscape(req.Model), url.QueryEscape(config.APIKey))
	raw, _, err := provider.DoJSONRequest(ctx, a.client, domain.ProviderGemini, http.MethodPost, endpoint, nil, payload)
	if err != nil {
		return nil, err
	}

	var response geminiResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	role, text, toolCalls := response.message()
	return &domain.UnifiedResponse{
		ResponseID: uuid.NewString(),
		Protocol:   req.Protocol,
		Provider:   domain.ProviderGemini,
		Model:      firstNonEmpty(response.ModelVersion, req.Model),
		Message: domain.UnifiedMessage{
			Role:      role,
			Text:      text,
			ToolCalls: toolCalls,
			Blocks: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: text,
			}},
		},
		Usage: domain.UsageRecord{
			InputTokens:  response.UsageMetadata.PromptTokenCount,
			OutputTokens: response.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  response.UsageMetadata.TotalTokenCount,
		},
		CreatedAt: time.Now().UTC(),
		Raw: map[string]any{
			"provider": "gemini",
		},
	}, nil
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
		"contents": buildContents(chatMessages),
	}
	if config := generationConfig(req); len(config) > 0 {
		payload["generationConfig"] = config
	}
	if systemText := provider.JoinSystem(systemParts); systemText != "" {
		payload["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": systemText}},
		}
	}
	if len(req.Tools) > 0 {
		payload["tools"] = buildTools(req.Tools)
	}
	endpoint := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse&key=%s", strings.TrimRight(config.BaseURL, "/"), url.PathEscape(req.Model), url.QueryEscape(config.APIKey))
	response, err := provider.DoStreamRequest(ctx, a.client, domain.ProviderGemini, http.MethodPost, endpoint, nil, payload)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	createdAt := time.Now().UTC()
	responseID := uuid.NewString()
	model := req.Model
	role := domain.RoleAssistant
	accumulatedText := ""
	toolCalls := []domain.ToolCall{}
	usage := domain.UsageRecord{}
	started := false
	emitStart := func() error {
		if started {
			return nil
		}
		started = true
		return emitStreamEvent(emit, domain.StreamEvent{
			Type:       domain.StreamEventResponseStart,
			ResponseID: responseID,
			Provider:   domain.ProviderGemini,
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
		var chunk geminiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return err
		}
		model = firstNonEmpty(chunk.ModelVersion, model)
		chunkRole, current, chunkToolCalls := chunk.message()
		role = chunkRole
		toolCalls = mergeToolCalls(toolCalls, chunkToolCalls)
		if err := emitStart(); err != nil {
			return err
		}
		delta := diffText(accumulatedText, current)
		if delta != "" {
			accumulatedText += delta
			if err := emitStreamEvent(emit, domain.StreamEvent{
				Type:       domain.StreamEventTextDelta,
				ResponseID: responseID,
				Provider:   domain.ProviderGemini,
				Model:      model,
				Role:       role,
				Delta:      delta,
				CreatedAt:  createdAt,
			}); err != nil {
				return err
			}
		}
		usage = domain.UsageRecord{
			InputTokens:  chunk.UsageMetadata.PromptTokenCount,
			OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  chunk.UsageMetadata.TotalTokenCount,
		}
		if usage.TotalTokens > 0 {
			if err := emitStreamEvent(emit, domain.StreamEvent{
				Type:       domain.StreamEventUsage,
				ResponseID: responseID,
				Provider:   domain.ProviderGemini,
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
	return &domain.UnifiedResponse{
		ResponseID: responseID,
		Protocol:   req.Protocol,
		Provider:   domain.ProviderGemini,
		Model:      model,
		Message: domain.UnifiedMessage{
			Role:      role,
			Text:      accumulatedText,
			ToolCalls: toolCalls,
			Blocks: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: accumulatedText,
			}},
		},
		Usage:     usage,
		CreatedAt: createdAt,
		Raw: map[string]any{
			"provider": "gemini",
		},
	}, nil
}

func (a *Adapter) resolveConfig(ctx context.Context) (provider.RuntimeConfig, error) {
	config, err := a.resolver.Resolve(ctx, domain.ProviderGemini)
	if err != nil {
		return provider.RuntimeConfig{}, err
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return provider.RuntimeConfig{}, fmt.Errorf("gemini api key is not configured")
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

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Role  string `json:"role"`
			Parts []struct {
				Text         string `json:"text"`
				FunctionCall *struct {
					ID   string         `json:"id"`
					Name string         `json:"name"`
					Args map[string]any `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
}

func (r geminiResponse) message() (domain.MessageRole, string, []domain.ToolCall) {
	if len(r.Candidates) == 0 {
		return domain.RoleAssistant, "", nil
	}
	role := responseRole(r.Candidates[0].Content.Role)
	parts := make([]string, 0, len(r.Candidates[0].Content.Parts))
	toolCalls := make([]domain.ToolCall, 0)
	for i, part := range r.Candidates[0].Content.Parts {
		if strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
		if part.FunctionCall != nil {
			callID := strings.TrimSpace(part.FunctionCall.ID)
			if callID == "" {
				callID = fmt.Sprintf("gemini-call-%d", i+1)
			}
			toolCalls = append(toolCalls, domain.ToolCall{
				ID:        callID,
				Name:      part.FunctionCall.Name,
				Arguments: part.FunctionCall.Args,
			})
		}
	}
	return role, strings.Join(parts, "\n"), toolCalls
}

func mergeToolCalls(existing, next []domain.ToolCall) []domain.ToolCall {
	if len(next) == 0 {
		return existing
	}
	out := append([]domain.ToolCall(nil), existing...)
	seen := map[string]struct{}{}
	for _, call := range out {
		key := firstNonEmpty(call.ID, call.Name)
		if strings.TrimSpace(key) != "" {
			seen[key] = struct{}{}
		}
	}
	for _, call := range next {
		key := firstNonEmpty(call.ID, call.Name)
		if strings.TrimSpace(key) == "" {
			out = append(out, call)
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, call)
	}
	return out
}

func buildContents(messages []domain.UnifiedMessage) []map[string]any {
	if len(messages) == 0 {
		return []map[string]any{{
			"role": "user",
			"parts": []map[string]any{{
				"text": "Continue.",
			}},
		}}
	}
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.Role == domain.RoleTool && len(message.ToolResults) > 0:
			parts := make([]map[string]any, 0, len(message.ToolResults))
			for _, result := range message.ToolResults {
				parts = append(parts, map[string]any{
					"functionResponse": map[string]any{
						"id":       result.ToolCallID,
						"name":     result.Name,
						"response": map[string]any{"output": result.Output},
					},
				})
			}
			out = append(out, map[string]any{
				"role":  "user",
				"parts": parts,
			})
		case message.Role == domain.RoleAssistant && len(message.ToolCalls) > 0:
			parts := make([]map[string]any, 0, len(message.ToolCalls)+1)
			parts = append(parts, geminiContentParts(message)...)
			for _, call := range message.ToolCalls {
				parts = append(parts, map[string]any{
					"functionCall": map[string]any{
						"id":   call.ID,
						"name": call.Name,
						"args": call.Arguments,
					},
				})
			}
			out = append(out, map[string]any{
				"role":  "model",
				"parts": parts,
			})
		default:
			parts := geminiContentParts(message)
			if len(parts) == 0 {
				continue
			}
			role := "user"
			if message.Role == domain.RoleAssistant {
				role = "model"
			}
			out = append(out, map[string]any{
				"role":  role,
				"parts": parts,
			})
		}
	}
	if len(out) == 0 {
		return []map[string]any{{
			"role": "user",
			"parts": []map[string]any{{
				"text": "Continue.",
			}},
		}}
	}
	return out
}

func geminiContentParts(message domain.UnifiedMessage) []map[string]any {
	blocks := message.Blocks
	if len(blocks) == 0 && strings.TrimSpace(message.Text) != "" {
		blocks = []domain.ContentBlock{{Type: domain.ContentBlockText, Text: message.Text}}
	}
	out := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case domain.ContentBlockText:
			if text := block.TextProxy(); text != "" {
				out = append(out, map[string]any{"text": text})
			}
		case domain.ContentBlockImage, domain.ContentBlockAudio, domain.ContentBlockVideo, domain.ContentBlockFile:
			if mimeType, data, ok := provider.InlineData(block.SourceURL); ok {
				out = append(out, map[string]any{"inlineData": map[string]any{"mimeType": mimeType, "data": data}})
				continue
			}
			if block.SourceURL != "" {
				out = append(out, map[string]any{
					"fileData": map[string]any{
						"mimeType": block.MIMEType,
						"fileUri":  block.SourceURL,
					},
				})
				continue
			}
			if text := block.TextProxy(); text != "" {
				out = append(out, map[string]any{"text": text})
			}
		default:
			if text := block.TextProxy(); text != "" {
				out = append(out, map[string]any{"text": text})
			}
		}
	}
	return out
}

func buildTools(tools []domain.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"functionDeclarations": []map[string]any{{
				"name":                 tool.Name,
				"description":          tool.Description,
				"parametersJsonSchema": tool.InputSchema,
			}},
		})
	}
	return out
}

func diffText(previous, current string) string {
	if previous == "" {
		return current
	}
	if current == previous {
		return ""
	}
	if strings.HasPrefix(current, previous) {
		return current[len(previous):]
	}
	return current
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

func responseRole(value string) domain.MessageRole {
	if value == "model" {
		return domain.RoleAssistant
	}
	if value == "user" {
		return domain.RoleUser
	}
	return domain.RoleAssistant
}
