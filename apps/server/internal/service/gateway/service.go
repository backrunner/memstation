package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/provider"
	conversationservice "memstation/apps/server/internal/service/conversation"
	"memstation/apps/server/internal/service/memory"
	settingsservice "memstation/apps/server/internal/service/settings"
)

type Service struct {
	providers        *provider.Registry
	memoryEngine     *memory.Engine
	conversations    *conversationservice.Service
	settings         *settingsservice.Service
	mu               sync.RWMutex
	defaultProvider  domain.Provider
	maxContextTokens int
	toolRuntime      *ToolRuntime
}

func NewService(providers *provider.Registry, memoryEngine *memory.Engine, conversations *conversationservice.Service, settings *settingsservice.Service, defaultProvider domain.Provider) *Service {
	return &Service{
		providers:        providers,
		memoryEngine:     memoryEngine,
		conversations:    conversations,
		settings:         settings,
		defaultProvider:  domain.NormalizeProvider(string(defaultProvider)),
		maxContextTokens: 0,
	}
}

func (s *Service) SetToolRuntime(runtime *ToolRuntime) {
	s.toolRuntime = runtime
}

func (s *Service) SetDefaultProvider(providerName domain.Provider) {
	providerName = domain.NormalizeProvider(string(providerName))
	if providerName == "" {
		return
	}
	s.mu.Lock()
	s.defaultProvider = providerName
	s.mu.Unlock()
}

func (s *Service) SetMaxContextTokens(tokens int) {
	if s == nil || tokens <= 0 {
		return
	}
	s.mu.Lock()
	s.maxContextTokens = tokens
	s.mu.Unlock()
}

func (s *Service) Proxy(ctx context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, domain.InjectionResult, error) {
	ctx, span := observability.TraceSpan(ctx, "gateway.proxy",
		attribute.String("gateway.protocol", string(req.Protocol)),
		attribute.String("gateway.mode", string(req.Mode)),
		attribute.Bool("gateway.stream", false),
	)
	defer span.End()

	req, injection, err := s.prepareRequest(ctx, req)
	if err != nil {
		return nil, domain.InjectionResult{}, err
	}

	updatedReq, response, err := s.completeRequest(ctx, req)
	if err != nil {
		return nil, injection, err
	}
	return s.finishRequest(ctx, updatedReq, response, injection)
}

func (s *Service) ProxyStream(ctx context.Context, req domain.UnifiedRequest, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, domain.InjectionResult, error) {
	ctx, span := observability.TraceSpan(ctx, "gateway.proxy_stream",
		attribute.String("gateway.protocol", string(req.Protocol)),
		attribute.String("gateway.mode", string(req.Mode)),
		attribute.Bool("gateway.stream", true),
	)
	defer span.End()

	req, injection, err := s.prepareRequest(ctx, req)
	if err != nil {
		return nil, domain.InjectionResult{}, err
	}

	updatedReq, response, err := s.streamRequest(ctx, req, emit)
	if err != nil {
		return nil, injection, err
	}
	return s.finishRequest(ctx, updatedReq, response, injection)
}

func (s *Service) ResolveRequestDefaults(ctx context.Context, req domain.UnifiedRequest) (domain.UnifiedRequest, error) {
	if req.RequestID == "" {
		req.RequestID = uuid.NewString()
	}
	if req.ReceivedAt.IsZero() {
		req.ReceivedAt = time.Now().UTC()
	}
	return s.resolveRequest(ctx, req)
}

func (s *Service) prepareRequest(ctx context.Context, req domain.UnifiedRequest) (domain.UnifiedRequest, domain.InjectionResult, error) {
	ctx, span := observability.TraceSpan(ctx, "gateway.prepare_request",
		attribute.String("gateway.protocol", string(req.Protocol)),
		attribute.String("gateway.user_id", req.UserID),
	)
	defer span.End()

	resolvedReq, err := s.ResolveRequestDefaults(ctx, req)
	if err != nil {
		return domain.UnifiedRequest{}, domain.InjectionResult{}, err
	}
	req = resolvedReq

	var injection domain.InjectionResult
	if req.Mode == domain.ModeInject {
		req, injection, err = s.memoryEngine.Inject(ctx, req)
		if err != nil {
			return domain.UnifiedRequest{}, domain.InjectionResult{}, fmt.Errorf("memory injection failed: %w", err)
		}
	}
	return req, injection, nil
}

func (s *Service) finishRequest(ctx context.Context, req domain.UnifiedRequest, response *domain.UnifiedResponse, injection domain.InjectionResult) (*domain.UnifiedResponse, domain.InjectionResult, error) {
	ctx, span := observability.TraceSpan(ctx, "gateway.finish_request",
		attribute.String("gateway.conversation_id", req.ConversationID),
		attribute.String("gateway.user_id", req.UserID),
	)
	defer span.End()

	if response.Model == "" {
		response.Model = req.Model
	}
	if req.RequestMetadata == nil {
		req.RequestMetadata = map[string]any{}
	}
	req.RequestMetadata["memory_persist_enabled"] = req.PersistMemoryEnabled()
	req.RequestMetadata["source_retain_enabled"] = req.SourceRetainEnabled(false)
	req.RequestMetadata["session_id"] = req.SessionID
	if serializedInjection := injectionMetadata(injection); len(serializedInjection) > 0 {
		req.RequestMetadata["injection"] = serializedInjection
	}
	conversationID, err := s.conversations.PersistExchange(ctx, req, response)
	if err != nil {
		return nil, injection, err
	}
	req.ConversationID = conversationID
	if response.Raw == nil {
		response.Raw = map[string]any{}
	}
	response.Raw["conversation_id"] = conversationID
	response.Raw["request_id"] = req.RequestID
	response.Raw["memory_persist_enabled"] = req.PersistMemoryEnabled()
	response.Raw["source_retain_enabled"] = req.SourceRetainEnabled(false)
	if serializedInjection := injectionMetadata(injection); len(serializedInjection) > 0 {
		response.Raw["injection"] = serializedInjection
	}
	if err := s.conversations.FinalizeIfNeeded(ctx, req, response); err != nil {
		return nil, injection, err
	}
	return response, injection, nil
}

func (s *Service) completeRequest(ctx context.Context, req domain.UnifiedRequest) (domain.UnifiedRequest, *domain.UnifiedResponse, error) {
	ctx, span := observability.TraceSpan(ctx, "gateway.complete_request",
		attribute.String("gateway.provider", string(req.Provider)),
		attribute.Int("gateway.tool_count", len(req.Tools)),
	)
	defer span.End()

	adapter, err := s.providers.Resolve(req.Provider)
	if err != nil {
		return domain.UnifiedRequest{}, nil, err
	}

	currentReq := req
	accumulatedUsage := domain.UsageRecord{}
	maxRounds := 1
	if s.toolRuntime != nil {
		maxRounds = s.toolRuntime.MaxRounds()
	}

	for round := 0; round < maxRounds; round++ {
		response, err := adapter.Complete(ctx, currentReq)
		if err != nil {
			return domain.UnifiedRequest{}, nil, err
		}
		accumulatedUsage = mergeUsage(accumulatedUsage, response.Usage)
		response.Usage = accumulatedUsage

		if s.toolRuntime == nil || len(response.Message.ToolCalls) == 0 || !s.toolRuntime.CanHandle(response.Message.ToolCalls) {
			return currentReq, response, nil
		}

		currentReq.Messages = append(currentReq.Messages, response.Message)
		toolMessages, err := s.toolRuntime.Execute(ctx, currentReq, response.Message)
		if err != nil {
			return domain.UnifiedRequest{}, nil, err
		}
		currentReq.Messages = append(currentReq.Messages, toolMessages...)
	}

	return domain.UnifiedRequest{}, nil, errors.New("tool execution exceeded maximum rounds")
}

func (s *Service) streamRequest(ctx context.Context, req domain.UnifiedRequest, emit func(domain.StreamEvent) error) (domain.UnifiedRequest, *domain.UnifiedResponse, error) {
	ctx, span := observability.TraceSpan(ctx, "gateway.stream_request",
		attribute.String("gateway.provider", string(req.Provider)),
		attribute.Int("gateway.tool_count", len(req.Tools)),
	)
	defer span.End()

	if s.toolRuntime != nil && s.toolRuntime.ShouldManageRequest(req) {
		updatedReq, response, err := s.completeRequest(ctx, req)
		if err != nil {
			return domain.UnifiedRequest{}, nil, err
		}
		if emit != nil {
			for _, event := range finalStreamEvents(response) {
				if err := emit(event); err != nil {
					return domain.UnifiedRequest{}, nil, err
				}
			}
		}
		return updatedReq, response, nil
	}

	adapter, err := s.providers.Resolve(req.Provider)
	if err != nil {
		return domain.UnifiedRequest{}, nil, err
	}
	response, err := adapter.Stream(ctx, req, emit)
	if err != nil {
		return domain.UnifiedRequest{}, nil, err
	}
	return req, response, nil
}

func finalStreamEvents(response *domain.UnifiedResponse) []domain.StreamEvent {
	if response == nil {
		return nil
	}
	role := response.Message.Role
	if role == "" {
		role = domain.RoleAssistant
	}
	events := []domain.StreamEvent{{
		Type:       domain.StreamEventResponseStart,
		ResponseID: response.ResponseID,
		Provider:   response.Provider,
		Model:      response.Model,
		Role:       role,
		CreatedAt:  response.CreatedAt,
	}}
	if response.Message.Text != "" {
		events = append(events, domain.StreamEvent{
			Type:       domain.StreamEventTextDelta,
			ResponseID: response.ResponseID,
			Provider:   response.Provider,
			Model:      response.Model,
			Role:       role,
			Delta:      response.Message.Text,
			CreatedAt:  response.CreatedAt,
		})
	}
	if response.Usage.TotalTokens > 0 {
		events = append(events, domain.StreamEvent{
			Type:       domain.StreamEventUsage,
			ResponseID: response.ResponseID,
			Provider:   response.Provider,
			Model:      response.Model,
			Role:       role,
			Usage:      response.Usage,
			CreatedAt:  response.CreatedAt,
		})
	}
	return events
}

func mergeUsage(left, right domain.UsageRecord) domain.UsageRecord {
	return domain.UsageRecord{
		InputTokens:  left.InputTokens + right.InputTokens,
		OutputTokens: left.OutputTokens + right.OutputTokens,
		TotalTokens:  left.TotalTokens + right.TotalTokens,
	}
}

func (s *Service) resolveRequest(ctx context.Context, req domain.UnifiedRequest) (domain.UnifiedRequest, error) {
	snapshot := map[string]any{}
	if s.settings != nil && req.UserID != "" {
		loaded, err := s.settings.SnapshotForUser(ctx, req.UserID)
		if err != nil {
			return domain.UnifiedRequest{}, err
		}
		snapshot = loaded
	}

	modelProvider, requestModel := parseModelReference(req.Model)
	settingProvider, settingModel := parseModelReference(stringValue(snapshot["chat_model"]))
	defaultProvider := s.currentDefaultProvider()

	switch {
	case req.Provider != "":
	case modelProvider != "":
		req.Provider = modelProvider
	case settingProvider != "":
		req.Provider = settingProvider
	default:
		req.Provider = protocolDefaultProvider(req.Protocol, defaultProvider)
	}

	if strings.TrimSpace(requestModel) != "" {
		req.Model = requestModel
	} else if strings.TrimSpace(settingModel) != "" && (settingProvider == "" || settingProvider == req.Provider) {
		req.Model = settingModel
	}

	if !req.PersistMemoryEnabled() {
		req.ToolWriteEnabled = false
	} else if !req.ToolWriteEnabled {
		req.ToolWriteEnabled = boolValue(snapshot["tool_write_enabled"])
	}
	if req.MaxInjectionTokens <= 0 {
		req.MaxInjectionTokens = intValue(snapshot["max_injection_tokens"])
		if req.MaxInjectionTokens <= 0 {
			req.MaxInjectionTokens = 2048
		}
	}
	if limit := s.currentMaxContextTokens(); limit > 0 && req.MaxInjectionTokens > limit {
		req.MaxInjectionTokens = limit
	}
	if req.SourceRetain == nil {
		req.SourceRetain = boolPtr(boolValue(snapshot["retain_source_materials"]))
	}

	return req, nil
}

func (s *Service) currentDefaultProvider() domain.Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaultProvider
}

func (s *Service) currentMaxContextTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maxContextTokens
}

func injectionMetadata(injection domain.InjectionResult) map[string]any {
	if injection.Query == "" && injection.Search.Total == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(injection.Search.Memories))
	for _, memory := range injection.Search.Memories {
		item := map[string]any{
			"id":              memory.ID,
			"node_id":         memory.NodeID,
			"type":            memory.Type,
			"content":         memory.Content,
			"score":           memory.Score,
			"effective_score": memory.EffectiveScore,
			"conversation_id": memory.ConversationID,
			"importance":      memory.Importance,
			"decay_factor":    memory.DecayFactor,
			"activity_at":     memory.ActivityAt,
			"time_tier":       memory.TimeTier,
		}
		if len(memory.Related) > 0 {
			item["related"] = memory.Related
		}
		if memory.Metadata != nil {
			if matchedKeywords, ok := memory.Metadata["matched_keywords"]; ok {
				item["matched_keywords"] = matchedKeywords
			}
			if keywords, ok := memory.Metadata["keywords"]; ok {
				item["keywords"] = keywords
			}
			for _, key := range []string{"injection_bucket", "score_reason", "topic_label", "temporal"} {
				if value, ok := memory.Metadata[key]; ok {
					item[key] = value
				}
			}
		}
		items = append(items, item)
	}
	return map[string]any{
		"query":                  injection.Query,
		"items":                  items,
		"total":                  injection.Search.Total,
		"query_embedding_tokens": injection.Search.QueryEmbeddingTokens,
	}
}

func parseModelReference(value string) (domain.Provider, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	for _, separator := range []string{":", "/"} {
		parts := strings.SplitN(value, separator, 2)
		if len(parts) != 2 {
			continue
		}
		if providerName, ok := domain.ParseProvider(parts[0]); ok {
			return providerName, strings.TrimSpace(parts[1])
		}
	}
	return "", value
}

func stringValue(value any) string {
	typed, _ := value.(string)
	return typed
}

func boolValue(value any) bool {
	typed, _ := value.(bool)
	return typed
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

func boolPtr(value bool) *bool {
	return &value
}

func protocolDefaultProvider(protocol domain.Protocol, fallback domain.Provider) domain.Provider {
	switch fallback {
	case domain.ProviderOpenAICompatible, domain.ProviderOpenAIResponses:
		switch protocol {
		case domain.ProtocolOpenAIResponses:
			return domain.ProviderOpenAIResponses
		case domain.ProtocolOpenAIChat:
			return domain.ProviderOpenAICompatible
		default:
			return fallback
		}
	default:
		return fallback
	}
}
