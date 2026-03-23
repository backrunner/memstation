package unit_test

import (
	"context"
	"testing"

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
	if result.Memories[0].NodeID != summary.NodeID {
		t.Fatalf("expected summary to rank first, got %s", result.Memories[0].NodeID)
	}
	foundRelated := false
	for _, item := range result.Memories {
		if item.NodeID == fact.NodeID {
			foundRelated = true
		}
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
