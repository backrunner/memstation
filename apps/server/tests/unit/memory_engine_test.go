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

type scriptedReranker struct {
	results   []domain.RerankResult
	lastInput domain.RerankInput
}

func (s *scriptedReranker) Rerank(_ context.Context, input domain.RerankInput) ([]domain.RerankResult, error) {
	s.lastInput = input
	return append([]domain.RerankResult{}, s.results...), nil
}

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

func TestMemoryEngineRerankerCanPromoteLowerRankedCandidate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	engine := memoryservice.NewEngine(repo, vectors)
	reranker := &scriptedReranker{}
	engine.SetReranker(reranker)

	docA := domain.MemoryDocument{
		ID:          "doc-a",
		NodeID:      "node-a",
		UserID:      "user-1",
		Type:        domain.MemoryTypeFact,
		Content:     "The release plan owner handles checklist reviews.",
		Importance:  1,
		DecayFactor: 1,
		UpdatedAt:   time.Now().UTC().Add(-2 * time.Hour),
	}
	docB := domain.MemoryDocument{
		ID:          "doc-b",
		NodeID:      "node-b",
		UserID:      "user-1",
		Type:        domain.MemoryTypeFact,
		Content:     "Alice is the rollback owner for production deploys.",
		Importance:  1,
		DecayFactor: 1,
		UpdatedAt:   time.Now().UTC().Add(-1 * time.Hour),
	}
	for _, doc := range []domain.MemoryDocument{docA, docB} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store document: %v", err)
		}
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

	reranker.results = []domain.RerankResult{
		{DocumentID: docB.NodeID, Score: 0.99},
		{DocumentID: docA.NodeID, Score: 0.05},
	}
	engine.SetRerankConfig(memoryservice.RerankConfig{
		Model:               "cohere:rerank-v3.5",
		CandidateMultiplier: 4,
		MinCandidates:       2,
		MaxCandidates:       10,
		BlendWeight:         1,
	})

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID: "user-1",
		Query:  docA.Content,
		TopN:   2,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Memories) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(result.Memories))
	}
	if got := result.Memories[0].NodeID; got != docB.NodeID {
		t.Fatalf("expected reranker to promote doc-b, got %s", got)
	}
	if reranker.lastInput.Model != "cohere:rerank-v3.5" {
		t.Fatalf("expected rerank model to flow through, got %s", reranker.lastInput.Model)
	}
	if len(reranker.lastInput.Documents) != 2 {
		t.Fatalf("expected 2 rerank candidates, got %d", len(reranker.lastInput.Documents))
	}
	if reranker.lastInput.Documents[0].Text == "" {
		t.Fatal("expected rerank candidate text to be populated")
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

func TestMemoryEngineIncludeConversationExpandsCompanionMemories(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	engine := memoryservice.NewEngine(repo, vectors)

	summary := domain.MemoryDocument{
		ID:             "summary:conv-include:v1",
		NodeID:         "node:summary:conv-include:v1",
		UserID:         "user-1",
		ConversationID: "conv-include",
		Type:           domain.MemoryTypeSummary,
		Content:        "Deployment freeze starts every Friday evening.",
		Importance:     1.1,
		DecayFactor:    1,
	}
	fact := domain.MemoryDocument{
		ID:             "fact:conv-include:freeze",
		NodeID:         "node:fact:conv-include:freeze",
		UserID:         "user-1",
		ConversationID: "conv-include",
		Type:           domain.MemoryTypeFact,
		Content:        "The freeze owner is Dana.",
		Importance:     1,
		DecayFactor:    1,
	}
	segment := domain.MemoryDocument{
		ID:             "segment:conv-include:000",
		NodeID:         "node:segment:conv-include:000",
		UserID:         "user-1",
		ConversationID: "conv-include",
		Type:           domain.MemoryTypeSegment,
		Content:        "user: remind me about the deployment freeze owner | assistant: noted",
		Importance:     0.9,
		DecayFactor:    0.85,
	}
	for _, doc := range []domain.MemoryDocument{summary, fact, segment} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store conversation doc %s: %v", doc.ID, err)
		}
	}
	if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
		UserID:           summary.UserID,
		NodeID:           summary.NodeID,
		NodeType:         string(summary.Type),
		EmbeddingVersion: 1,
		EmbeddingModel:   "test-embedding",
		ContentHash:      summary.Content,
		Vector:           vector.EmbedText(summary.Content),
	}); err != nil {
		t.Fatalf("upsert summary vector: %v", err)
	}

	withoutConversation, err := engine.Search(ctx, domain.SearchInput{
		UserID: "user-1",
		Query:  summary.Content,
		TopN:   5,
	})
	if err != nil {
		t.Fatalf("search without conversation expansion: %v", err)
	}
	if len(withoutConversation.Memories) != 1 || withoutConversation.Memories[0].NodeID != summary.NodeID {
		t.Fatalf("expected only direct summary hit without include_conversation, got %+v", withoutConversation.Memories)
	}

	withConversation, err := engine.Search(ctx, domain.SearchInput{
		UserID:              "user-1",
		Query:               summary.Content,
		TopN:                5,
		IncludeConversation: true,
	})
	if err != nil {
		t.Fatalf("search with conversation expansion: %v", err)
	}
	foundFact := false
	foundSegment := false
	for _, item := range withConversation.Memories {
		if item.NodeID == fact.NodeID {
			foundFact = true
			if expanded, ok := item.Metadata["conversation_expanded"].(bool); !ok || !expanded {
				t.Fatalf("expected fact companion to be marked as conversation expanded, got %+v", item.Metadata)
			}
		}
		if item.NodeID == segment.NodeID {
			foundSegment = true
		}
	}
	if !foundFact {
		t.Fatalf("expected same-conversation fact companion, got %+v", withConversation.Memories)
	}
	if foundSegment {
		t.Fatalf("expected raw segment companion to stay hidden when IncludeSegments=false, got %+v", withConversation.Memories)
	}

	withConversationAndSegments, err := engine.Search(ctx, domain.SearchInput{
		UserID:              "user-1",
		Query:               summary.Content,
		TopN:                5,
		IncludeConversation: true,
		IncludeSegments:     true,
	})
	if err != nil {
		t.Fatalf("search with conversation+segment expansion: %v", err)
	}
	foundSegment = false
	for _, item := range withConversationAndSegments.Memories {
		if item.NodeID == segment.NodeID {
			foundSegment = true
			if expanded, ok := item.Metadata["conversation_expanded"].(bool); !ok || !expanded {
				t.Fatalf("expected segment companion to be marked as conversation expanded, got %+v", item.Metadata)
			}
		}
	}
	if !foundSegment {
		t.Fatalf("expected raw segment companion when include_conversation and include_segments are both enabled, got %+v", withConversationAndSegments.Memories)
	}
}

func TestMemoryEngineFallbackIncludeConversationExpandsCompanionMemories(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	engine := memoryservice.NewEngine(repo, nil)

	summary := domain.MemoryDocument{
		ID:             "summary:conv-fallback:v1",
		NodeID:         "node:summary:conv-fallback:v1",
		UserID:         "user-1",
		ConversationID: "conv-fallback",
		Type:           domain.MemoryTypeSummary,
		Content:        "Runbook mentions the canary checklist.",
		Importance:     1.1,
		DecayFactor:    1,
	}
	impression := domain.MemoryDocument{
		ID:             "impression:conv-fallback:ops",
		NodeID:         "node:impression:conv-fallback:ops",
		UserID:         "user-1",
		ConversationID: "conv-fallback",
		Type:           domain.MemoryTypeImpression,
		Content:        "The team sounded cautious about the rollout.",
		Importance:     0.9,
		DecayFactor:    0.95,
	}
	for _, doc := range []domain.MemoryDocument{summary, impression} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store fallback conversation doc %s: %v", doc.ID, err)
		}
	}

	withoutConversation, err := engine.Search(ctx, domain.SearchInput{
		UserID: "user-1",
		Query:  "canary checklist",
		TopN:   5,
	})
	if err != nil {
		t.Fatalf("fallback search without conversation expansion: %v", err)
	}
	if len(withoutConversation.Memories) != 1 || withoutConversation.Memories[0].NodeID != summary.NodeID {
		t.Fatalf("expected only lexical summary hit without include_conversation, got %+v", withoutConversation.Memories)
	}

	withConversation, err := engine.Search(ctx, domain.SearchInput{
		UserID:              "user-1",
		Query:               "canary checklist",
		TopN:                5,
		IncludeConversation: true,
	})
	if err != nil {
		t.Fatalf("fallback search with conversation expansion: %v", err)
	}
	foundImpression := false
	for _, item := range withConversation.Memories {
		if item.NodeID == impression.NodeID {
			foundImpression = true
			if expanded, ok := item.Metadata["conversation_expanded"].(bool); !ok || !expanded {
				t.Fatalf("expected fallback companion to be marked as conversation expanded, got %+v", item.Metadata)
			}
		}
	}
	if !foundImpression {
		t.Fatalf("expected fallback search to include same-conversation impression, got %+v", withConversation.Memories)
	}
}

func TestMemoryEngineCanonicalizesSupersededFactMatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	engine := memoryservice.NewEngine(repo, nil)

	older := domain.MemoryDocument{
		ID:             "fact-city-old",
		NodeID:         "node:fact-city-old",
		UserID:         "user-1",
		ConversationID: "conv-city",
		Type:           domain.MemoryTypeFact,
		Content:        "The user lives in Hangzhou.",
		Importance:     1,
		DecayFactor:    1,
		Metadata: map[string]any{
			"fact_key":         "home_location",
			"superseded_by_id": "fact-city-current",
		},
	}
	current := domain.MemoryDocument{
		ID:             "fact-city-current",
		NodeID:         "node:fact-city-current",
		UserID:         "user-1",
		ConversationID: "conv-city",
		Type:           domain.MemoryTypeFact,
		Content:        "The user now lives in Shanghai.",
		Importance:     1.1,
		DecayFactor:    1,
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	if err := repo.StoreDocument(ctx, current); err != nil {
		t.Fatalf("store current fact: %v", err)
	}
	if err := repo.StoreDocument(ctx, older); err != nil {
		t.Fatalf("store older fact: %v", err)
	}

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID:      "user-1",
		Query:       older.Content,
		TopN:        3,
		MemoryTypes: []domain.MemoryType{domain.MemoryTypeFact},
	})
	if err != nil {
		t.Fatalf("search superseded fact: %v", err)
	}
	if len(result.Memories) != 1 {
		t.Fatalf("expected canonicalized single fact result, got %+v", result.Memories)
	}
	if result.Memories[0].NodeID != current.NodeID {
		t.Fatalf("expected current fact to replace stale fact, got %+v", result.Memories)
	}
	if canonicalized, ok := result.Memories[0].Metadata["supersession_canonicalized"].(bool); !ok || !canonicalized {
		t.Fatalf("expected current fact to be marked as canonicalized, got %+v", result.Memories[0].Metadata)
	}
	if aliases, ok := result.Memories[0].Metadata["superseded_alias_node_ids"].([]string); !ok || len(aliases) != 1 || aliases[0] != older.NodeID {
		t.Fatalf("expected superseded alias metadata to include stale node, got %+v", result.Memories[0].Metadata)
	}
}

func TestMemoryEngineGraphExpansionCanonicalizesSupersededFacts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	engine := memoryservice.NewEngine(repo, vectors)

	summary := domain.MemoryDocument{
		ID:             "summary:city-history:v1",
		NodeID:         "node:summary:city-history:v1",
		UserID:         "user-1",
		ConversationID: "conv-city-history",
		Type:           domain.MemoryTypeSummary,
		Content:        "Conversation covered the user's relocation history and current home city.",
		Importance:     1.1,
		DecayFactor:    1,
	}
	older := domain.MemoryDocument{
		ID:             "fact-city-history-old",
		NodeID:         "node:fact-city-history-old",
		UserID:         "user-1",
		ConversationID: "conv-city-history",
		Type:           domain.MemoryTypeFact,
		Content:        "The user lived in Hangzhou.",
		Importance:     1,
		DecayFactor:    1,
		Metadata: map[string]any{
			"fact_key":         "home_location",
			"superseded_by_id": "fact-city-history-current",
		},
	}
	current := domain.MemoryDocument{
		ID:             "fact-city-history-current",
		NodeID:         "node:fact-city-history-current",
		UserID:         "user-1",
		ConversationID: "conv-city-history",
		Type:           domain.MemoryTypeFact,
		Content:        "The user now lives in Shanghai.",
		Importance:     1.1,
		DecayFactor:    1,
		Metadata: map[string]any{
			"fact_key": "home_location",
		},
	}
	for _, doc := range []domain.MemoryDocument{summary, current, older} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store graph doc %s: %v", doc.ID, err)
		}
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
		UserID:       "user-1",
		SrcNodeID:    summary.NodeID,
		DstNodeID:    older.NodeID,
		RelationType: "derived_from",
		Weight:       0.9,
		Source:       "test",
	}); err != nil {
		t.Fatalf("link summary to stale fact: %v", err)
	}
	if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
		UserID:           summary.UserID,
		NodeID:           summary.NodeID,
		NodeType:         string(summary.Type),
		EmbeddingVersion: 1,
		EmbeddingModel:   "test-embedding",
		ContentHash:      summary.Content,
		Vector:           vector.EmbedText(summary.Content),
	}); err != nil {
		t.Fatalf("upsert summary vector: %v", err)
	}

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID: "user-1",
		Query:  summary.Content,
		TopN:   3,
	})
	if err != nil {
		t.Fatalf("search graph canonicalization: %v", err)
	}

	foundCurrent := false
	foundStale := false
	for _, item := range result.Memories {
		if item.NodeID == current.NodeID {
			foundCurrent = true
			if canonicalized, ok := item.Metadata["supersession_canonicalized"].(bool); !ok || !canonicalized {
				t.Fatalf("expected current fact to be marked as canonicalized, got %+v", item.Metadata)
			}
		}
		if item.NodeID == older.NodeID {
			foundStale = true
		}
	}
	if !foundCurrent {
		t.Fatalf("expected graph expansion to surface current fact, got %+v", result.Memories)
	}
	if foundStale {
		t.Fatalf("expected stale fact to be collapsed out of graph results, got %+v", result.Memories)
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

func TestMemoryEngineSearchPenalizesDistantRelationHops(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	engine := memoryservice.NewEngine(repo, vectors)
	engine.SetRelationRecallConfig(memoryservice.RelationRecallConfig{
		MaxHops:          3,
		HopDecayLambda:   0.35,
		HopDecayExponent: 1.25,
	})

	seed := domain.MemoryDocument{
		ID:          "summary:graph-hop:seed",
		NodeID:      "node:graph-hop:seed",
		UserID:      "user-1",
		Type:        domain.MemoryTypeSummary,
		Content:     "primary rollout recall anchor",
		Importance:  1,
		DecayFactor: 1,
	}
	hop1 := domain.MemoryDocument{
		ID:          "fact:graph-hop:1",
		NodeID:      "node:graph-hop:1",
		UserID:      "user-1",
		Type:        domain.MemoryTypeFact,
		Content:     "first hop related memory",
		Importance:  1,
		DecayFactor: 1,
	}
	hop2 := domain.MemoryDocument{
		ID:          "fact:graph-hop:2",
		NodeID:      "node:graph-hop:2",
		UserID:      "user-1",
		Type:        domain.MemoryTypeFact,
		Content:     "second hop related memory",
		Importance:  1,
		DecayFactor: 1,
	}
	hop3 := domain.MemoryDocument{
		ID:          "fact:graph-hop:3",
		NodeID:      "node:graph-hop:3",
		UserID:      "user-1",
		Type:        domain.MemoryTypeFact,
		Content:     "third hop related memory",
		Importance:  1,
		DecayFactor: 1,
	}
	for _, doc := range []domain.MemoryDocument{seed, hop1, hop2, hop3} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store graph doc %s: %v", doc.ID, err)
		}
	}
	for _, relation := range []domain.MemoryRelation{
		{UserID: "user-1", SrcNodeID: seed.NodeID, DstNodeID: hop1.NodeID, RelationType: "derived_from", Weight: 1, Source: "test"},
		{UserID: "user-1", SrcNodeID: hop1.NodeID, DstNodeID: hop2.NodeID, RelationType: "derived_from", Weight: 1, Source: "test"},
		{UserID: "user-1", SrcNodeID: hop2.NodeID, DstNodeID: hop3.NodeID, RelationType: "derived_from", Weight: 1, Source: "test"},
	} {
		if err := repo.LinkDocuments(ctx, relation); err != nil {
			t.Fatalf("link graph relation %+v: %v", relation, err)
		}
	}
	if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
		UserID:           seed.UserID,
		NodeID:           seed.NodeID,
		NodeType:         string(seed.Type),
		EmbeddingVersion: 1,
		EmbeddingModel:   "test-embedding",
		ContentHash:      seed.Content,
		Vector:           vector.EmbedText(seed.Content),
	}); err != nil {
		t.Fatalf("upsert seed vector: %v", err)
	}

	result, err := engine.Search(ctx, domain.SearchInput{
		UserID: "user-1",
		Query:  seed.Content,
		TopN:   4,
	})
	if err != nil {
		t.Fatalf("search graph hop penalty: %v", err)
	}

	positions := map[string]int{}
	for idx, item := range result.Memories {
		positions[item.NodeID] = idx
	}
	for _, nodeID := range []string{hop1.NodeID, hop2.NodeID, hop3.NodeID} {
		if _, ok := positions[nodeID]; !ok {
			t.Fatalf("expected recalled node %s in results, got %+v", nodeID, result.Memories)
		}
	}
	if positions[hop1.NodeID] >= positions[hop2.NodeID] {
		t.Fatalf("expected 1-hop memory to rank above 2-hop memory, got %+v", result.Memories)
	}
	if positions[hop2.NodeID] >= positions[hop3.NodeID] {
		t.Fatalf("expected 2-hop memory to rank above 3-hop memory, got %+v", result.Memories)
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
