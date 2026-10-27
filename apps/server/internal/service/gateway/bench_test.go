package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	conversationservice "memstation/apps/server/internal/service/conversation"
	memoryservice "memstation/apps/server/internal/service/memory"
	settingsservice "memstation/apps/server/internal/service/settings"
	vectorstore "memstation/apps/server/internal/store/vector"
)

// stubProviderAdapter returns a fixed response without any network calls so
// benchmarks isolate MemStation's gateway overhead from upstream latency.
type stubProviderAdapter struct {
	name domain.Provider
}

func (s stubProviderAdapter) Name() domain.Provider { return s.name }

func (s stubProviderAdapter) Complete(ctx context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	return &domain.UnifiedResponse{
		ResponseID: "resp-stub",
		Protocol:   req.Protocol,
		Provider:   s.name,
		Model:      req.Model,
		Message: domain.UnifiedMessage{
			Role: domain.RoleAssistant,
			Text: "stub response",
		},
		Usage: domain.UsageRecord{
			InputTokens: 42, OutputTokens: 18, TotalTokens: 60,
		},
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (s stubProviderAdapter) Stream(ctx context.Context, req domain.UnifiedRequest, emit func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	// Emit a single delta then close. The first-token-emit path is what plan
	// Product targets measure this path against the 250ms P95 stream budget.
	if emit != nil {
		if err := emit(domain.StreamEvent{
			Type:       domain.StreamEventTextDelta,
			ResponseID: "resp-stub",
			Delta:      "stub",
			CreatedAt:  time.Now().UTC(),
		}); err != nil {
			return nil, err
		}
	}
	return s.Complete(ctx, req)
}

// BenchmarkProxyNonStreamOverhead measures the gateway overhead on the
// transparent non-stream path with a stubbed provider. Plan target:
// P95 < 150ms.
func BenchmarkProxyNonStreamOverhead(b *testing.B) {
	svc := setupGatewayBench(b)
	req := newBenchRequest(false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, _, err := svc.Proxy(context.Background(), req)
		if err != nil {
			b.Fatalf("proxy: %v", err)
		}
		if resp == nil || resp.Usage.TotalTokens == 0 {
			b.Fatalf("expected stub usage, got %+v", resp)
		}
	}
}

// BenchmarkProxyStreamOverhead measures the gateway overhead on the stream
// path including first-token emit + persist + finalize. Plan target:
// P95 < 250ms first-token.
func BenchmarkProxyStreamOverhead(b *testing.B) {
	svc := setupGatewayBench(b)
	req := newBenchRequest(true)
	emit := func(domain.StreamEvent) error { return nil }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := svc.ProxyStream(context.Background(), req, emit); err != nil {
			b.Fatalf("proxy stream: %v", err)
		}
	}
}

// BenchmarkProxyInjectOverhead measures the end-to-end inject path including
// memory recall + context injection + persist. It captures the realistic
// worst-case gateway overhead for memory-aware requests.
func BenchmarkProxyInjectOverhead(b *testing.B) {
	svc := setupGatewayBench(b)
	req := newBenchRequest(false)
	req.Mode = domain.ModeInject
	req.InjectContext = true
	req.MaxInjectionTokens = 1024
	persist := false
	req.MemoryPersist = &persist
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := svc.Proxy(context.Background(), req); err != nil {
			b.Fatalf("proxy inject: %v", err)
		}
	}
}

// BenchmarkResolveRequestDefaults measures the header/settings resolution that
// every request goes through before dispatching to a provider. The hot path
// is called on every gateway request, so keeping it cheap matters.
func BenchmarkResolveRequestDefaults(b *testing.B) {
	svc := setupGatewayBench(b)
	req := newBenchRequest(false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.ResolveRequestDefaults(context.Background(), req); err != nil {
			b.Fatalf("resolve: %v", err)
		}
	}
}

func setupGatewayBench(b *testing.B) *Service {
	b.Helper()
	ctx := context.Background()

	memRepo := memorystore.NewInMemoryMemoryRepository()
	vectors := vectorstore.NewPGVectorStore(nil)
	engine := memoryservice.NewEngine(memRepo, vectors)

	// Seed a small corpus so inject-mode finds something to rank.
	now := time.Now().UTC()
	for i := 0; i < 24; i++ {
		doc := domain.MemoryDocument{
			ID:         fmt.Sprintf("bench-doc-%d", i),
			NodeID:     fmt.Sprintf("bench-node-%d", i),
			UserID:     "bench-user",
			Type:       domain.MemoryTypeSummary,
			Content:    fmt.Sprintf("summary %d about the rollout plan and the incident retrospective", i),
			Importance: 1.0, DecayFactor: 0.95,
			ActivityAt:     now.Add(-time.Duration(i) * time.Hour),
			UpdatedAt:      now.Add(-time.Duration(i) * time.Hour),
			EffectiveScore: 0.8,
		}
		_ = memRepo.StoreDocument(ctx, doc)
		_ = vectors.Upsert(ctx, domain.VectorUpsertRequest{
			UserID:   doc.UserID,
			NodeID:   doc.NodeID,
			NodeType: string(doc.Type),
			Content:  doc.Content,
		})
	}

	convRepo := conversationstore.NewInMemoryConversationRepository()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	conversations := conversationservice.NewService(convRepo, outbox, 32, 64000)

	settings := settingsservice.NewService(nil, nil, "gpt-5", "gpt-5-mini", "gpt-5-mini", "text-embedding-3-large", 2048)

	registry := provider.NewRegistry(domain.ProviderOpenAICompatible, stubProviderAdapter{name: domain.ProviderOpenAICompatible})
	svc := NewService(registry, engine, conversations, settings, domain.ProviderOpenAICompatible)
	return svc
}

func newBenchRequest(stream bool) domain.UnifiedRequest {
	return domain.UnifiedRequest{
		UserID:   "bench-user",
		Protocol: domain.ProtocolOpenAIChat,
		Mode:     domain.ModeTransparent,
		Provider: domain.ProviderOpenAICompatible,
		Model:    "gpt-5",
		Stream:   stream,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "Remind me what we decided about the rollout plan."},
		}, AgentID: "fixture-agent",
	}
}
