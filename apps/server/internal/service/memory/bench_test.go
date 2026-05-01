package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	sqlstore "memstation/apps/server/internal/store/sql"
	vectorstore "memstation/apps/server/internal/store/vector"
)

// BenchmarkMemorySearchSmallCorpus measures end-to-end memory search on a
// small corpus: the common single-user case. The product target
// targets memory-search P95 < 250ms.
func BenchmarkMemorySearchSmallCorpus(b *testing.B) {
	engine, input := setupEngineBench(b, 50)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Search(context.Background(), input); err != nil {
			b.Fatalf("search: %v", err)
		}
	}
}

// BenchmarkMemorySearchMediumCorpus measures search latency on a realistic
// mid-size corpus (five hundred stored memories for one user).
func BenchmarkMemorySearchMediumCorpus(b *testing.B) {
	engine, input := setupEngineBench(b, 500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Search(context.Background(), input); err != nil {
			b.Fatalf("search: %v", err)
		}
	}
}

// BenchmarkMemorySearchLargeCorpus stresses search on a large-ish corpus so
// regressions in the ranking hot path show up clearly.
func BenchmarkMemorySearchLargeCorpus(b *testing.B) {
	engine, input := setupEngineBench(b, 2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.Search(context.Background(), input); err != nil {
			b.Fatalf("search: %v", err)
		}
	}
}

// BenchmarkInject measures the full inject pipeline (recall + system-prompt
// assembly + tool injection) per the inject-mode hot path in memory-system.md.
func BenchmarkInject(b *testing.B) {
	engine, _ := setupEngineBench(b, 200)
	req := domain.UnifiedRequest{
		UserID:             "bench-user",
		Mode:               domain.ModeInject,
		InjectContext:      true,
		InjectTools:        true,
		MaxInjectionTokens: 2048,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "Remind me what we decided about the rollout plan."},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := engine.Inject(context.Background(), req); err != nil {
			b.Fatalf("inject: %v", err)
		}
	}
}

// BenchmarkExpandRelationScores isolates the graph-expansion hot path. This
// corresponds to the planned 1-2 hop traversal.
func BenchmarkExpandRelationScores(b *testing.B) {
	seeds := make(map[string]float64, 50)
	var relations []domain.MemoryRelation
	for i := 0; i < 50; i++ {
		seedID := fmt.Sprintf("seed-%d", i)
		seeds[seedID] = 0.9
		for j := 0; j < 8; j++ {
			relations = append(relations, domain.MemoryRelation{
				SrcNodeID:    seedID,
				DstNodeID:    fmt.Sprintf("hop-%d-%d", i, j),
				RelationType: "derived_from",
				Weight:       0.6,
			})
		}
	}
	cfg := RelationRecallConfig{MaxHops: 2, HopDecayLambda: 0.35, HopDecayExponent: 1.25}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		expandRelationScores(seeds, relations, cfg)
	}
}

// BenchmarkScoreDocument isolates the per-document score computation, which
// runs inside every search and drives effective_score ranking.
func BenchmarkScoreDocument(b *testing.B) {
	doc := domain.MemoryDocument{
		ID:         "doc-1",
		NodeID:     "node-1",
		Type:       domain.MemoryTypeSummary,
		Content:    "Shipped the payments rollout and closed all blockers from the incident channel.",
		Importance: 1.2, DecayFactor: 0.92,
		ActivityAt:     time.Now().Add(-36 * time.Hour),
		UpdatedAt:      time.Now(),
		EffectiveScore: 0.8,
	}
	cfg := configForVariant(SearchVariantFull)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scoreDocument(doc, 0.78, 0.24, 0.12, 0.05, 0.05, 0.0, 0.0, cfg)
	}
}

// BenchmarkPackInjectionMemories measures the injection packer that enforces
// the max_injection_tokens budget.
func BenchmarkPackInjectionMemories(b *testing.B) {
	memories := make([]domain.MemoryDocument, 0, 32)
	for i := 0; i < 32; i++ {
		typ := domain.MemoryTypeSummary
		switch i % 4 {
		case 0:
			typ = domain.MemoryTypeKeyMemory
		case 1:
			typ = domain.MemoryTypeFact
		case 2:
			typ = domain.MemoryTypeImpression
		}
		memories = append(memories, domain.MemoryDocument{
			ID:             fmt.Sprintf("doc-%d", i),
			NodeID:         fmt.Sprintf("node-%d", i),
			Type:           typ,
			Content:        fmt.Sprintf("Memory %d about the active rollout and the deploy plan we discussed earlier.", i),
			EffectiveScore: 1.0 - float64(i)*0.02,
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		packInjectionMemories(memories, 2048)
	}
}

// BenchmarkBuildInjectionSystemPrompt measures the structured system-prompt
// assembly used by inject-mode requests.
func BenchmarkBuildInjectionSystemPrompt(b *testing.B) {
	memories := []domain.MemoryDocument{
		{Type: domain.MemoryTypeKeyMemory, Content: "UTC+8", Metadata: map[string]any{"key": "timezone"}},
		{Type: domain.MemoryTypeKeyMemory, Content: "Go", Metadata: map[string]any{"key": "language"}},
		{Type: domain.MemoryTypeSummary, Content: "Rolled out the new memory ranking on staging this week."},
		{Type: domain.MemoryTypeSummary, Content: "Debugged the worker deadlock together last Friday."},
		{Type: domain.MemoryTypeFact, Content: "The oncall rotation is weekly on Mondays."},
		{Type: domain.MemoryTypeImpression, Content: "User prefers terse answers and explicit file paths."},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildInjectionSystemPrompt(memories)
	}
}

// setupEngineBench seeds an in-memory repo + vector store with a realistic
// distribution of memory types so the search hot path covers every branch
// (key memory, summary, fact, impression, segments, relations).
func setupEngineBench(b *testing.B, total int) (*Engine, domain.SearchInput) {
	b.Helper()
	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	vectors := vectorstore.NewPGVectorStore(nil)
	engine := NewEngine(repo, vectors)

	now := time.Now().UTC()
	userID := "bench-user"
	for i := 0; i < total; i++ {
		var typ domain.MemoryType
		switch i % 5 {
		case 0:
			typ = domain.MemoryTypeKeyMemory
		case 1:
			typ = domain.MemoryTypeSummary
		case 2:
			typ = domain.MemoryTypeSegment
		case 3:
			typ = domain.MemoryTypeFact
		default:
			typ = domain.MemoryTypeImpression
		}
		doc := domain.MemoryDocument{
			ID:             fmt.Sprintf("doc-%d", i),
			NodeID:         fmt.Sprintf("node-%d", i),
			UserID:         userID,
			ConversationID: fmt.Sprintf("conv-%d", i%16),
			Type:           typ,
			Content:        fmt.Sprintf("rollout plan detail %d: we shipped the payments migration and the embedding rebuild job", i),
			Importance:     1.0,
			DecayFactor:    0.95,
			Score:          0.6 + float64(i%10)*0.02,
			EffectiveScore: 0.6 + float64(i%10)*0.02,
			ActivityAt:     now.Add(-time.Duration(i) * time.Hour),
			UpdatedAt:      now.Add(-time.Duration(i) * time.Hour),
		}
		if typ == domain.MemoryTypeKeyMemory {
			doc.Metadata = map[string]any{"key": fmt.Sprintf("preference_%d", i)}
		}
		if err := repo.StoreDocument(ctx, doc); err != nil {
			b.Fatalf("store document %d: %v", i, err)
		}
		if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
			UserID:   userID,
			NodeID:   doc.NodeID,
			NodeType: string(typ),
			Content:  doc.Content,
		}); err != nil {
			b.Fatalf("vector upsert %d: %v", i, err)
		}
	}

	return engine, domain.SearchInput{
		UserID:          userID,
		Query:           "rollout plan payments migration",
		TopN:            10,
		IncludeSegments: true,
	}
}
