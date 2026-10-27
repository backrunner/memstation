package unit_test

import (
	"context"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	auditsvc "memstation/apps/server/internal/service/audit"
	conversationservice "memstation/apps/server/internal/service/conversation"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	memoryservice "memstation/apps/server/internal/service/memory"
	settingsservice "memstation/apps/server/internal/service/settings"
	"memstation/apps/server/internal/store/vector"
	"memstation/apps/server/tests/testutil/mockprovider"
)

func TestGatewayUsesUserSettingProviderAndModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conversations := conversationstore.NewInMemoryConversationRepository()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	memories := memorystore.NewInMemoryMemoryRepository()
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	_, err := settings.UpdateForUser(ctx, "user-1", map[string]any{
		"chat_model": "anthropic:claude-3-7-sonnet",
	})
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}

	service := gatewayservice.NewService(
		provider.NewRegistry(
			domain.ProviderOpenAICompatible,
			mockprovider.New(domain.ProviderOpenAICompatible),
			mockprovider.New(domain.ProviderAnthropic),
		),
		memoryservice.NewEngine(memories, vector.NewPGVectorStore(nil)),
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAICompatible,
	)

	response, _, err := service.Proxy(ctx, domain.UnifiedRequest{
		UserID:   "user-1",
		Protocol: domain.ProtocolOpenAIChat,
		Messages: []domain.UnifiedMessage{{
			Role: domain.RoleUser,
			Text: "hello",
		}}, AgentID: "fixture-agent",
	})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	if response.Provider != domain.ProviderAnthropic {
		t.Fatalf("expected anthropic provider, got %s", response.Provider)
	}
	if response.Model != "claude-3-7-sonnet" {
		t.Fatalf("expected model from user settings, got %s", response.Model)
	}
}

func TestGatewayExplicitProviderOverridesUserSetting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conversations := conversationstore.NewInMemoryConversationRepository()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	memories := memorystore.NewInMemoryMemoryRepository()
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	_, err := settings.UpdateForUser(ctx, "user-1", map[string]any{
		"chat_model": "anthropic:claude-3-7-sonnet",
	})
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}

	service := gatewayservice.NewService(
		provider.NewRegistry(
			domain.ProviderOpenAICompatible,
			mockprovider.New(domain.ProviderOpenAICompatible),
			mockprovider.New(domain.ProviderAnthropic),
		),
		memoryservice.NewEngine(memories, vector.NewPGVectorStore(nil)),
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAICompatible,
	)

	response, _, err := service.Proxy(ctx, domain.UnifiedRequest{
		UserID:   "user-1",
		Protocol: domain.ProtocolOpenAIChat,
		Provider: domain.ProviderOpenAICompatible,
		Model:    "gpt-4.1-mini",
		Messages: []domain.UnifiedMessage{{
			Role: domain.RoleUser,
			Text: "hello",
		}}, AgentID: "fixture-agent",
	})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	if response.Provider != domain.ProviderOpenAICompatible {
		t.Fatalf("expected openai provider, got %s", response.Provider)
	}
	if response.Model != "gpt-4.1-mini" {
		t.Fatalf("expected explicit request model, got %s", response.Model)
	}
}

func TestGatewayProxyStreamPersistsAndFinalizesConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conversations := conversationstore.NewInMemoryConversationRepository()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	memories := memorystore.NewInMemoryMemoryRepository()
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)

	service := gatewayservice.NewService(
		provider.NewRegistry(
			domain.ProviderOpenAICompatible,
			mockprovider.New(domain.ProviderOpenAICompatible),
		),
		memoryservice.NewEngine(memories, vector.NewPGVectorStore(nil)),
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAICompatible,
	)

	events := make([]domain.StreamEvent, 0, 3)
	response, _, err := service.ProxyStream(ctx, domain.UnifiedRequest{
		UserID:          "user-1",
		Protocol:        domain.ProtocolOpenAIChat,
		Model:           "gpt-4.1-mini",
		Stream:          true,
		EndConversation: true,
		Messages: []domain.UnifiedMessage{{
			Role: domain.RoleUser,
			Text: "hello",
		}}, AgentID: "fixture-agent",
	}, func(event domain.StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("proxy stream: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected stream events to be emitted")
	}
	list, err := conversations.ListConversations(ctx, "user-1")
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(list))
	}
	if list[0].FinalizeStatus != domain.FinalizeStatusEnding {
		t.Fatalf("expected conversation to enter ending state, got %s", list[0].FinalizeStatus)
	}
	pending, err := outbox.Pending(ctx, 10)
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(pending))
	}
	if response.Message.Text == "" {
		t.Fatal("expected streamed response text to be persisted")
	}
}

func TestGatewayExecutesInjectedMemoryToolCalls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conversations := conversationstore.NewInMemoryConversationRepository()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	memories := memorystore.NewInMemoryMemoryRepository()
	if err := memories.StoreDocument(ctx, domain.MemoryDocument{
		ID:      "summary-1",
		NodeID:  "node-summary-1",
		UserID:  "user-1",
		Type:    domain.MemoryTypeSummary,
		Content: "The alpha roadmap focuses on shipping the MemStation gateway.",
	}); err != nil {
		t.Fatalf("store memory: %v", err)
	}

	engine := memoryservice.NewEngine(memories, vector.NewPGVectorStore(nil))
	memorySvc := memoryservice.NewService(engine, memories)
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	adapter := &sequenceAdapter{
		provider: domain.ProviderOpenAICompatible,
		steps: []func(domain.UnifiedRequest) *domain.UnifiedResponse{
			func(req domain.UnifiedRequest) *domain.UnifiedResponse {
				if len(req.Tools) == 0 {
					t.Fatal("expected injected tools to be present")
				}
				return &domain.UnifiedResponse{
					ResponseID: "resp-tool-1",
					Protocol:   req.Protocol,
					Provider:   domain.ProviderOpenAICompatible,
					Model:      "gpt-4.1-mini",
					Message: domain.UnifiedMessage{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "call-1",
							Name: "recall_memory",
							Arguments: map[string]any{
								"query": "alpha roadmap",
								"top_n": 2,
							},
						}},
					},
					Usage: domain.UsageRecord{InputTokens: 10, OutputTokens: 3, TotalTokens: 13},
				}
			},
			func(req domain.UnifiedRequest) *domain.UnifiedResponse {
				foundToolResult := false
				for _, message := range req.Messages {
					if message.Role == domain.RoleTool && len(message.ToolResults) > 0 {
						foundToolResult = true
					}
				}
				if !foundToolResult {
					t.Fatal("expected tool result message in follow-up request")
				}
				return &domain.UnifiedResponse{
					ResponseID: "resp-tool-2",
					Protocol:   req.Protocol,
					Provider:   domain.ProviderOpenAICompatible,
					Model:      "gpt-4.1-mini",
					Message: domain.UnifiedMessage{
						Role: domain.RoleAssistant,
						Text: "I found the alpha roadmap memory and used it in the answer.",
					},
					Usage: domain.UsageRecord{InputTokens: 12, OutputTokens: 9, TotalTokens: 21},
				}
			},
		},
	}

	service := gatewayservice.NewService(
		provider.NewRegistry(domain.ProviderOpenAICompatible, adapter),
		engine,
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAICompatible,
	)
	service.SetToolRuntime(gatewayservice.NewToolRuntime(memorySvc, audit, map[string]struct{}{
		"recent_conversation": {},
	}, 3))

	response, _, err := service.Proxy(ctx, domain.UnifiedRequest{
		UserID:      "user-1",
		Protocol:    domain.ProtocolOpenAIChat,
		Model:       "gpt-4.1-mini",
		Mode:        domain.ModeInject,
		InjectTools: true,
		Messages:    []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "What do you remember about the alpha roadmap?"}},
		ReceivedAt:  now(), AgentID: "fixture-agent",
	})
	if err != nil {
		t.Fatalf("proxy with tools: %v", err)
	}
	if adapter.calls != 2 {
		t.Fatalf("expected 2 provider rounds, got %d", adapter.calls)
	}
	if response.Message.Text == "" {
		t.Fatal("expected final assistant response")
	}
	if response.Usage.TotalTokens != 34 {
		t.Fatalf("expected aggregated usage, got %+v", response.Usage)
	}
	list, err := conversations.ListConversations(ctx, "user-1")
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected persisted conversation, got %d", len(list))
	}
	conversation, err := conversations.GetConversation(ctx, "user-1", list[0].ConversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(conversation.Messages) < 4 {
		t.Fatalf("expected tool roundtrip messages to persist, got %d", len(conversation.Messages))
	}
}

func TestGatewayExecutesWhitelistedWriteKeyMemoryTool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conversations := conversationstore.NewInMemoryConversationRepository()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	memories := memorystore.NewInMemoryMemoryRepository()
	engine := memoryservice.NewEngine(memories, vector.NewPGVectorStore(nil))
	memorySvc := memoryservice.NewService(engine, memories)
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	adapter := &sequenceAdapter{
		provider: domain.ProviderOpenAICompatible,
		steps: []func(domain.UnifiedRequest) *domain.UnifiedResponse{
			func(req domain.UnifiedRequest) *domain.UnifiedResponse {
				return &domain.UnifiedResponse{
					ResponseID: "resp-write-1",
					Protocol:   req.Protocol,
					Provider:   domain.ProviderOpenAICompatible,
					Model:      "gpt-4.1-mini",
					Message: domain.UnifiedMessage{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "call-write-1",
							Name: "write_key_memory",
							Arguments: map[string]any{
								"key":   "recent_conversation",
								"value": "conv-123",
							},
						}},
					},
					Usage: domain.UsageRecord{InputTokens: 5, OutputTokens: 2, TotalTokens: 7},
				}
			},
			func(req domain.UnifiedRequest) *domain.UnifiedResponse {
				return &domain.UnifiedResponse{
					ResponseID: "resp-write-2",
					Protocol:   req.Protocol,
					Provider:   domain.ProviderOpenAICompatible,
					Model:      "gpt-4.1-mini",
					Message: domain.UnifiedMessage{
						Role: domain.RoleAssistant,
						Text: "Stored the recent conversation key memory.",
					},
					Usage: domain.UsageRecord{InputTokens: 6, OutputTokens: 6, TotalTokens: 12},
				}
			},
		},
	}

	service := gatewayservice.NewService(
		provider.NewRegistry(domain.ProviderOpenAICompatible, adapter),
		engine,
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAICompatible,
	)
	service.SetToolRuntime(gatewayservice.NewToolRuntime(memorySvc, audit, map[string]struct{}{
		"recent_conversation": {},
	}, 3))

	response, _, err := service.Proxy(ctx, domain.UnifiedRequest{
		UserID:           "user-1",
		Protocol:         domain.ProtocolOpenAIChat,
		Model:            "gpt-4.1-mini",
		Mode:             domain.ModeInject,
		InjectTools:      true,
		ToolWriteEnabled: true,
		Messages:         []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Remember the latest conversation id."}},
		ReceivedAt:       now(), AgentID: "fixture-agent",
	})
	if err != nil {
		t.Fatalf("proxy with write tool: %v", err)
	}
	if response.Message.Text == "" {
		t.Fatal("expected final response")
	}
	keyMemories, err := memorySvc.ListKeyMemories(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list key memories: %v", err)
	}
	if len(keyMemories) != 1 || keyMemories[0].Key != "recent_conversation" {
		t.Fatalf("expected persisted key memory, got %+v", keyMemories)
	}
	events := audit.List(ctx, 10)
	if len(events) == 0 || events[0].Action != "tool_write_key_memory" {
		t.Fatalf("expected tool write audit event, got %+v", events)
	}
}

func TestGatewayDisablesMemoryPersistenceAndToolWritesPerRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conversations := conversationstore.NewInMemoryConversationRepository()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	memories := memorystore.NewInMemoryMemoryRepository()
	engine := memoryservice.NewEngine(memories, vector.NewPGVectorStore(nil))
	memorySvc := memoryservice.NewService(engine, memories)
	settings := settingsservice.NewService(nil, nil, "", "", "", "", 2048)
	audit := auditsvc.NewService(nil)
	adapter := &sequenceAdapter{
		provider: domain.ProviderOpenAICompatible,
		steps: []func(domain.UnifiedRequest) *domain.UnifiedResponse{
			func(req domain.UnifiedRequest) *domain.UnifiedResponse {
				return &domain.UnifiedResponse{
					ResponseID: "resp-persist-1",
					Protocol:   req.Protocol,
					Provider:   domain.ProviderOpenAICompatible,
					Model:      "gpt-4.1-mini",
					Message: domain.UnifiedMessage{
						Role: domain.RoleAssistant,
						ToolCalls: []domain.ToolCall{{
							ID:   "call-persist-1",
							Name: "write_key_memory",
							Arguments: map[string]any{
								"key":   "recent_conversation",
								"value": "conv-disabled",
							},
						}},
					},
					Usage: domain.UsageRecord{InputTokens: 4, OutputTokens: 2, TotalTokens: 6},
				}
			},
			func(req domain.UnifiedRequest) *domain.UnifiedResponse {
				foundFailure := false
				for _, message := range req.Messages {
					if message.Role != domain.RoleTool || len(message.ToolResults) == 0 {
						continue
					}
					output, ok := message.ToolResults[0].Output.(map[string]any)
					if !ok {
						continue
					}
					if got := output["error"]; got == "memory persistence is disabled for this request" {
						foundFailure = true
					}
				}
				if !foundFailure {
					t.Fatal("expected tool write failure when memory persistence is disabled")
				}
				return &domain.UnifiedResponse{
					ResponseID: "resp-persist-2",
					Protocol:   req.Protocol,
					Provider:   domain.ProviderOpenAICompatible,
					Model:      "gpt-4.1-mini",
					Message: domain.UnifiedMessage{
						Role: domain.RoleAssistant,
						Text: "Memory persistence is disabled for this session.",
					},
					Usage: domain.UsageRecord{InputTokens: 5, OutputTokens: 4, TotalTokens: 9},
				}
			},
		},
	}

	service := gatewayservice.NewService(
		provider.NewRegistry(domain.ProviderOpenAICompatible, adapter),
		engine,
		conversationservice.NewService(conversations, outbox, 100, 100000),
		settings,
		domain.ProviderOpenAICompatible,
	)
	service.SetToolRuntime(gatewayservice.NewToolRuntime(memorySvc, audit, map[string]struct{}{
		"recent_conversation": {},
	}, 3))

	response, _, err := service.Proxy(ctx, domain.UnifiedRequest{
		UserID:           "user-1",
		Protocol:         domain.ProtocolOpenAIChat,
		Model:            "gpt-4.1-mini",
		Mode:             domain.ModeInject,
		InjectTools:      true,
		ToolWriteEnabled: true,
		MemoryPersist:    boolRef(false),
		SourceRetain:     boolRef(false),
		EndConversation:  true,
		Messages:         []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Remember this conversation."}},
		ReceivedAt:       now(), AgentID: "fixture-agent",
	})
	if err != nil {
		t.Fatalf("proxy with disabled persistence: %v", err)
	}
	if response.Message.Text == "" {
		t.Fatal("expected final response")
	}

	keyMemories, err := memorySvc.ListKeyMemories(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list key memories: %v", err)
	}
	if len(keyMemories) != 0 {
		t.Fatalf("expected no key memory writes, got %+v", keyMemories)
	}

	list, err := conversations.ListConversations(ctx, "user-1")
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(list))
	}
	conversation, err := conversations.GetConversation(ctx, "user-1", list[0].ConversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if conversation.FinalizeStatus != domain.FinalizeStatusEnded {
		t.Fatalf("expected conversation ended without queue, got %s", conversation.FinalizeStatus)
	}
	if conversation.SummaryStatus != domain.ProcessingStatusSkipped || conversation.AnalysisStatus != domain.ProcessingStatusSkipped {
		t.Fatalf("expected skipped processing statuses, got summary=%s analysis=%s", conversation.SummaryStatus, conversation.AnalysisStatus)
	}

	pending, err := outbox.Pending(ctx, 10)
	if err != nil {
		t.Fatalf("list outbox: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no outbox events, got %d", len(pending))
	}

	events := audit.List(ctx, 10)
	for _, event := range events {
		if event.Action == "tool_write_key_memory" {
			t.Fatalf("unexpected tool write audit event when persistence disabled: %+v", event)
		}
	}
}

type sequenceAdapter struct {
	provider domain.Provider
	steps    []func(domain.UnifiedRequest) *domain.UnifiedResponse
	calls    int
}

func (a *sequenceAdapter) Name() domain.Provider { return a.provider }

func (a *sequenceAdapter) Complete(_ context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	if a.calls >= len(a.steps) {
		return nil, nil
	}
	a.calls++
	return a.steps[a.calls-1](req), nil
}

func (a *sequenceAdapter) Stream(context.Context, domain.UnifiedRequest, func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	return nil, nil
}

func now() time.Time { return time.Now().UTC() }

func boolRef(value bool) *bool { return &value }
