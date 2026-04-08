package unit_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/store/sql"
	"memstation/apps/server/internal/store/vector"
)

func TestMemoryEngineVectorSearchExpandsRelations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	engine := memoryservice.NewEngine(repo, vectors)
	service := memoryservice.NewService(engine, repo)
	service.SetVectorStore(vectors)

	summary := domain.MemoryDocument{
		ID:          uuid.NewString(),
		NodeID:      uuid.NewString(),
		UserID:      "user-1",
		Type:        domain.MemoryTypeSummary,
		Content:     "Alice likes hiking and weekend trails.",
		Importance:  1.2,
		DecayFactor: 1,
	}
	fact := domain.MemoryDocument{
		ID:          uuid.NewString(),
		NodeID:      uuid.NewString(),
		UserID:      "user-1",
		Type:        domain.MemoryTypeFact,
		Content:     "Alice currently lives in Shanghai.",
		Importance:  1.0,
		DecayFactor: 1,
	}

	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatalf("store summary: %v", err)
	}
	if err := repo.StoreDocument(ctx, fact); err != nil {
		t.Fatalf("store fact: %v", err)
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
		UserID:       "user-1",
		SrcNodeID:    summary.NodeID,
		DstNodeID:    fact.NodeID,
		RelationType: "derived_from",
		Weight:       0.9,
		Source:       "test",
	}); err != nil {
		t.Fatalf("link documents: %v", err)
	}

	for _, doc := range []domain.MemoryDocument{summary, fact} {
		if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
			UserID:           doc.UserID,
			NodeID:           doc.NodeID,
			NodeType:         string(doc.Type),
			EmbeddingVersion: 1,
			EmbeddingModel:   "test-embedding",
			ContentHash:      doc.Content,
			Vector:           vector.EmbedText(doc.Content),
		}); err != nil {
			t.Fatalf("upsert vector: %v", err)
		}
	}

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID: "user-1",
		Query:  summary.Content,
		TopN:   2,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Memories) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(result.Memories))
	}
	foundSummary := false
	foundRelated := false
	for _, item := range result.Memories {
		if item.NodeID == summary.NodeID {
			foundSummary = true
		}
		if item.NodeID == fact.NodeID {
			foundRelated = true
		}
	}
	if !foundSummary {
		t.Fatal("expected source summary to remain in the top results")
	}
	if !foundRelated {
		t.Fatal("expected related fact to be returned from relation expansion")
	}
}

func TestMemoryEngineKeywordSearchExpandsGraphWithoutVectors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	engine := memoryservice.NewEngine(repo, nil)
	service := memoryservice.NewService(engine, repo)

	summary := domain.MemoryDocument{
		ID:             uuid.NewString(),
		NodeID:         uuid.NewString(),
		UserID:         "user-1",
		ConversationID: "conv-1",
		Type:           domain.MemoryTypeSummary,
		Content:        "Release checklist covers rollback owner and incident contacts.",
		Metadata: map[string]any{
			"keywords": []string{"release", "rollback", "owner"},
		},
		Importance:  1.2,
		DecayFactor: 1,
	}
	fact := domain.MemoryDocument{
		ID:             uuid.NewString(),
		NodeID:         uuid.NewString(),
		UserID:         "user-1",
		ConversationID: "conv-1",
		Type:           domain.MemoryTypeFact,
		Content:        "Alice is the rollback owner.",
		Importance:     1,
		DecayFactor:    1,
	}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatalf("store summary: %v", err)
	}
	if err := repo.StoreDocument(ctx, fact); err != nil {
		t.Fatalf("store fact: %v", err)
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
		UserID:       "user-1",
		SrcNodeID:    summary.NodeID,
		DstNodeID:    fact.NodeID,
		RelationType: "derived_from",
		Weight:       0.9,
		Source:       "test",
	}); err != nil {
		t.Fatalf("link summary to fact: %v", err)
	}
	if _, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "recent_conversation",
		Value:  "conv-1",
		Source: "worker",
	}); err != nil {
		t.Fatalf("upsert key memory: %v", err)
	}

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID: "user-1",
		Query:  "rollback owner",
		TopN:   5,
	})
	if err != nil {
		t.Fatalf("keyword search: %v", err)
	}
	if len(result.Memories) < 3 {
		t.Fatalf("expected summary, related fact, and key memory, got %d", len(result.Memories))
	}

	foundFact := false
	foundKeyMemory := false
	foundKeywordMetadata := false
	for _, item := range result.Memories {
		if item.Type == domain.MemoryTypeFact {
			foundFact = true
		}
		if item.Type == domain.MemoryTypeKeyMemory {
			foundKeyMemory = true
		}
		if item.Type == domain.MemoryTypeSummary {
			if matched, ok := item.Metadata["matched_keywords"].([]string); ok && len(matched) > 0 {
				foundKeywordMetadata = true
			}
		}
	}
	if !foundFact {
		t.Fatal("expected related fact from keyword-seeded graph expansion")
	}
	if !foundKeyMemory {
		t.Fatal("expected related key memory from keyword-seeded graph expansion")
	}
	if !foundKeywordMetadata {
		t.Fatal("expected summary result to include matched_keywords metadata")
	}
}

func TestMemoryEngineUsesSegmentGraphForTwoHopExpansion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	engine := memoryservice.NewEngine(repo, vectors)

	segment := domain.MemoryDocument{
		ID:             "segment:conv-1:000",
		NodeID:         "node:segment:conv-1:000",
		UserID:         "user-1",
		ConversationID: "conv-1",
		Type:           domain.MemoryTypeSegment,
		Content:        "user: Alice prefers alpine hiking on weekends | assistant: preference recorded",
		Importance:     0.95,
		DecayFactor:    0.9,
	}
	summary := domain.MemoryDocument{
		ID:             "summary:conv-1:v1",
		NodeID:         "node:summary:conv-1:v1",
		UserID:         "user-1",
		ConversationID: "conv-1",
		Type:           domain.MemoryTypeSummary,
		Content:        "Conversation captured Alice's personal preferences.",
		Importance:     1.1,
		DecayFactor:    1,
	}
	fact := domain.MemoryDocument{
		ID:             "fact:conv-1:preference",
		NodeID:         "node:fact:conv-1:preference",
		UserID:         "user-1",
		ConversationID: "conv-1",
		Type:           domain.MemoryTypeFact,
		Content:        "Alice enjoys hiking.",
		Importance:     1,
		DecayFactor:    1,
	}
	for _, doc := range []domain.MemoryDocument{segment, summary, fact} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store memory doc %s: %v", doc.ID, err)
		}
	}
	for _, relation := range []domain.MemoryRelation{
		{
			UserID:       "user-1",
			SrcNodeID:    segment.NodeID,
			DstNodeID:    summary.NodeID,
			RelationType: "summarized_into",
			Weight:       0.92,
			Source:       "test",
		},
		{
			UserID:       "user-1",
			SrcNodeID:    summary.NodeID,
			DstNodeID:    fact.NodeID,
			RelationType: "derived_from",
			Weight:       0.9,
			Source:       "test",
		},
	} {
		if err := repo.LinkDocuments(ctx, relation); err != nil {
			t.Fatalf("link relation %+v: %v", relation, err)
		}
	}
	if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
		UserID:           segment.UserID,
		NodeID:           segment.NodeID,
		NodeType:         string(segment.Type),
		EmbeddingVersion: 1,
		EmbeddingModel:   "test-embedding",
		ContentHash:      segment.Content,
		Vector:           vector.EmbedText(segment.Content),
	}); err != nil {
		t.Fatalf("upsert segment vector: %v", err)
	}

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID:          "user-1",
		Query:           "alpine hiking weekends",
		TopN:            3,
		IncludeSegments: false,
	})
	if err != nil {
		t.Fatalf("search with segment graph: %v", err)
	}

	foundSummary := false
	foundFact := false
	for _, item := range result.Memories {
		if item.Type == domain.MemoryTypeSegment {
			t.Fatalf("expected raw segment to stay hidden when IncludeSegments=false, got %+v", result.Memories)
		}
		if item.NodeID == summary.NodeID {
			foundSummary = true
		}
		if item.NodeID == fact.NodeID {
			foundFact = true
		}
	}
	if !foundSummary {
		t.Fatalf("expected summary to be recalled via segment graph, got %+v", result.Memories)
	}
	if !foundFact {
		t.Fatalf("expected fact to be recalled through 2-hop segment graph expansion, got %+v", result.Memories)
	}
}

func TestMemoryEngineIncludesSegmentsWhenRequested(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	engine := memoryservice.NewEngine(repo, vectors)

	segment := domain.MemoryDocument{
		ID:             "segment:conv-2:000",
		NodeID:         "node:segment:conv-2:000",
		UserID:         "user-1",
		ConversationID: "conv-2",
		Type:           domain.MemoryTypeSegment,
		Content:        "user: keep the deployment playbook visible | assistant: acknowledged",
		Importance:     0.9,
		DecayFactor:    0.85,
	}
	if err := repo.StoreDocument(ctx, segment); err != nil {
		t.Fatalf("store segment doc: %v", err)
	}
	if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
		UserID:           segment.UserID,
		NodeID:           segment.NodeID,
		NodeType:         string(segment.Type),
		EmbeddingVersion: 1,
		EmbeddingModel:   "test-embedding",
		ContentHash:      segment.Content,
		Vector:           vector.EmbedText(segment.Content),
	}); err != nil {
		t.Fatalf("upsert segment vector: %v", err)
	}

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID:          "user-1",
		Query:           "deployment playbook visible",
		TopN:            3,
		IncludeSegments: true,
	})
	if err != nil {
		t.Fatalf("search including segments: %v", err)
	}
	if len(result.Memories) == 0 {
		t.Fatal("expected at least one search result")
	}
	if result.Memories[0].Type != domain.MemoryTypeSegment {
		t.Fatalf("expected segment result to be returned when IncludeSegments=true, got %+v", result.Memories)
	}
}

func TestMemoryEngineSearchWithoutVectorsResortsByVariantScore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	engine := memoryservice.NewEngine(repo, nil)
	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)

	highImportance := domain.MemoryDocument{
		ID:          "summary:high-importance",
		NodeID:      "node:summary:high-importance",
		UserID:      "user-1",
		Type:        domain.MemoryTypeSummary,
		Content:     "deployment playbook rollout",
		Importance:  2,
		DecayFactor: 1,
		CreatedAt:   now.Add(-10 * 24 * time.Hour),
		UpdatedAt:   now.Add(-10 * 24 * time.Hour),
	}
	freshLowImportance := domain.MemoryDocument{
		ID:          "summary:fresh-low-importance",
		NodeID:      "node:summary:fresh-low-importance",
		UserID:      "user-1",
		Type:        domain.MemoryTypeSummary,
		Content:     "deployment playbook rollout",
		Importance:  0.5,
		DecayFactor: 1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	for _, doc := range []domain.MemoryDocument{highImportance, freshLowImportance} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store fallback doc %s: %v", doc.ID, err)
		}
	}

	fullResult, err := engine.SearchWithVariant(ctx, domain.SearchInput{
		UserID: "user-1",
		Query:  "deployment playbook rollout",
		TopN:   2,
	}, memoryservice.SearchVariantFull)
	if err != nil {
		t.Fatalf("full search without vectors: %v", err)
	}
	if len(fullResult.Result.Memories) != 2 || fullResult.Result.Memories[0].ID != highImportance.ID {
		t.Fatalf("expected full ranking to favor high-importance document, got %+v", fullResult.Result.Memories)
	}

	semanticOnlyResult, err := engine.SearchWithVariant(ctx, domain.SearchInput{
		UserID: "user-1",
		Query:  "deployment playbook rollout",
		TopN:   2,
	}, memoryservice.SearchVariantSemanticOnly)
	if err != nil {
		t.Fatalf("semantic-only search without vectors: %v", err)
	}
	if len(semanticOnlyResult.Result.Memories) != 2 || semanticOnlyResult.Result.Memories[0].ID != freshLowImportance.ID {
		t.Fatalf("expected semantic-only ranking to fall back to newer document after score tie, got %+v", semanticOnlyResult.Result.Memories)
	}
}

func TestMemoryEngineSearchWithoutVectorsAppliesEffectiveMinScore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	engine := memoryservice.NewEngine(repo, nil)
	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)

	if err := repo.StoreDocument(ctx, domain.MemoryDocument{
		ID:          "summary:weak-effective-score",
		NodeID:      "node:summary:weak-effective-score",
		UserID:      "user-1",
		Type:        domain.MemoryTypeSummary,
		Content:     "incident runbook",
		Importance:  0.5,
		DecayFactor: 0.5,
		CreatedAt:   now.Add(-120 * 24 * time.Hour),
		UpdatedAt:   now.Add(-120 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("store low-score fallback doc: %v", err)
	}

	result, err := engine.SearchWithVariant(ctx, domain.SearchInput{
		UserID:   "user-1",
		Query:    "incident runbook",
		TopN:     5,
		MinScore: 0.5,
	}, memoryservice.SearchVariantFull)
	if err != nil {
		t.Fatalf("search with effective min score: %v", err)
	}
	if len(result.Result.Memories) != 0 {
		t.Fatalf("expected low effective-score fallback result to be filtered out, got %+v", result.Result.Memories)
	}
}

func TestMemoryEngineSearchWithoutVectorsHonorsRequestedTypesWithoutSegmentBackfill(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	engine := memoryservice.NewEngine(repo, nil)
	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)

	segment := domain.MemoryDocument{
		ID:             "segment:conv-typed:000",
		NodeID:         "node:segment:conv-typed:000",
		UserID:         "user-1",
		ConversationID: "conv-typed",
		Type:           domain.MemoryTypeSegment,
		Content:        "deployment runbook ownership",
		Importance:     2,
		DecayFactor:    1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	summary := domain.MemoryDocument{
		ID:             "summary:conv-typed:v1",
		NodeID:         "node:summary:conv-typed:v1",
		UserID:         "user-1",
		ConversationID: "conv-typed",
		Type:           domain.MemoryTypeSummary,
		Content:        "deployment runbook ownership",
		Importance:     1,
		DecayFactor:    1,
		CreatedAt:      now.Add(-1 * time.Hour),
		UpdatedAt:      now.Add(-1 * time.Hour),
	}
	for _, doc := range []domain.MemoryDocument{segment, summary} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store typed fallback doc %s: %v", doc.ID, err)
		}
	}

	result, err := engine.SearchWithVariant(ctx, domain.SearchInput{
		UserID:      "user-1",
		Query:       "deployment runbook ownership",
		TopN:        1,
		MemoryTypes: []domain.MemoryType{domain.MemoryTypeSummary},
	}, memoryservice.SearchVariantFull)
	if err != nil {
		t.Fatalf("typed fallback search: %v", err)
	}
	if len(result.Result.Memories) != 1 || result.Result.Memories[0].ID != summary.ID {
		t.Fatalf("expected summary-only fallback search to keep the requested type, got %+v", result.Result.Memories)
	}
}

func TestMemoryEngineSearchIncludesGenerationlessKeyMemoriesAlongsideScopedDocs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	engine := memoryservice.NewEngine(repo, vectors)
	service := memoryservice.NewService(engine, repo)
	service.SetVectorStore(vectors)

	summary := domain.MemoryDocument{
		ID:           "summary:key-memory-mixed:v1",
		NodeID:       "node:summary:key-memory-mixed:v1",
		GenerationID: "gen:current:user-1",
		UserID:       "user-1",
		Type:         domain.MemoryTypeSummary,
		Content:      "Preferred code style is explicit Go naming and small interfaces.",
		Importance:   1.2,
		DecayFactor:  1,
	}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatalf("store scoped summary: %v", err)
	}
	if _, err := service.UpsertKeyMemory(ctx, domain.KeyMemory{
		UserID: "user-1",
		Key:    "preferred_code_style",
		Value:  "explicit Go naming and small interfaces",
		Source: "test",
	}); err != nil {
		t.Fatalf("upsert generationless key memory: %v", err)
	}
	if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
		GenerationID:     summary.GenerationID,
		UserID:           summary.UserID,
		NodeID:           summary.NodeID,
		NodeType:         string(summary.Type),
		EmbeddingVersion: 1,
		EmbeddingModel:   "test-embedding",
		ContentHash:      summary.Content,
		Vector:           vector.EmbedText(summary.Content),
	}); err != nil {
		t.Fatalf("upsert scoped summary vector: %v", err)
	}

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID:      "user-1",
		Query:       "preferred_code_style: explicit Go naming and small interfaces",
		TopN:        5,
		MemoryTypes: []domain.MemoryType{domain.MemoryTypeSummary, domain.MemoryTypeKeyMemory},
	})
	if err != nil {
		t.Fatalf("mixed scoped search: %v", err)
	}

	foundSummary := false
	foundKeyMemory := false
	for _, item := range result.Memories {
		if item.Type == domain.MemoryTypeSummary {
			foundSummary = true
		}
		if item.Type == domain.MemoryTypeKeyMemory {
			foundKeyMemory = true
		}
	}
	if !foundSummary || !foundKeyMemory {
		t.Fatalf("expected scoped summary and generationless key memory in mixed search, got %+v", result.Memories)
	}
}
