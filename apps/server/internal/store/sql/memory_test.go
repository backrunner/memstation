package sql

import (
	"context"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

func TestInMemoryMemoryRepositoryDeleteSegmentsByConversationUsesKeepSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	now := time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC)

	keepDoc := domain.MemoryDocument{
		ID:             "segment:conv-keep:000",
		NodeID:         "node:segment:conv-keep:000",
		UserID:         "user-1",
		ConversationID: "conv-keep",
		Type:           domain.MemoryTypeSegment,
		Content:        "user: keep this segment",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	staleDoc := domain.MemoryDocument{
		ID:             "segment:conv-keep:001",
		NodeID:         "node:segment:conv-keep:001",
		UserID:         "user-1",
		ConversationID: "conv-keep",
		Type:           domain.MemoryTypeSegment,
		Content:        "user: remove this segment",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	for _, doc := range []domain.MemoryDocument{keepDoc, staleDoc} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store doc %s: %v", doc.ID, err)
		}
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
		UserID:       "user-1",
		SrcNodeID:    staleDoc.NodeID,
		DstNodeID:    "node:summary:conv-keep:v1",
		RelationType: "summarized_into",
		Weight:       1,
		Source:       "test",
	}); err != nil {
		t.Fatalf("seed stale relation: %v", err)
	}

	deletedNodeIDs, err := repo.DeleteSegmentsByConversation(ctx, "user-1", "conv-keep", []string{keepDoc.ID})
	if err != nil {
		t.Fatalf("delete stale segments: %v", err)
	}
	if len(deletedNodeIDs) != 1 || deletedNodeIDs[0] != staleDoc.NodeID {
		t.Fatalf("expected stale node %s to be deleted, got %+v", staleDoc.NodeID, deletedNodeIDs)
	}
	if _, err := repo.GetDocument(ctx, "user-1", keepDoc.ID, domain.MemoryTypeSegment); err != nil {
		t.Fatalf("expected keep doc to remain: %v", err)
	}
	if _, err := repo.GetDocument(ctx, "user-1", staleDoc.ID, domain.MemoryTypeSegment); err == nil {
		t.Fatalf("expected stale doc to be removed")
	}
	relations, err := repo.ListRelations(ctx, "user-1", "", []string{staleDoc.NodeID}, 1)
	if err != nil {
		t.Fatalf("list stale relations: %v", err)
	}
	if len(relations) != 0 {
		t.Fatalf("expected stale relations to be removed, got %+v", relations)
	}
}

func TestInMemoryMemoryRepositoryListRelationsDoesNotExpandThroughTemporalNodes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	now := time.Date(2026, 4, 9, 9, 0, 0, 0, time.UTC)

	root := domain.MemoryDocument{
		ID:        "summary-root",
		NodeID:    "node-summary-root",
		UserID:    "user-1",
		Type:      domain.MemoryTypeSummary,
		Content:   "Root memory document.",
		CreatedAt: now,
		UpdatedAt: now,
	}
	neighbor := domain.MemoryDocument{
		ID:        "summary-neighbor",
		NodeID:    "node-summary-neighbor",
		UserID:    "user-1",
		Type:      domain.MemoryTypeSummary,
		Content:   "Neighbor memory document.",
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, doc := range []domain.MemoryDocument{root, neighbor} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store doc %s: %v", doc.ID, err)
		}
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{
		UserID:       "user-1",
		SrcNodeID:    neighbor.NodeID,
		DstNodeID:    "node-fact-neighbor",
		RelationType: "derived_from",
		Weight:       0.9,
		Source:       "test",
	}); err != nil {
		t.Fatalf("link neighbor relation: %v", err)
	}

	relations, err := repo.ListRelations(ctx, "user-1", "", []string{root.NodeID}, 2)
	if err != nil {
		t.Fatalf("list relations: %v", err)
	}
	for _, relation := range relations {
		if relation.RelationType == "derived_from" {
			t.Fatalf("expected temporal frontier walk to stay within the root graph, got %+v", relations)
		}
	}
}

func TestInMemoryMemoryRepositorySearchKeywordMatchesUsesSummaryContent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryMemoryRepository()
	now := time.Date(2026, 4, 9, 9, 0, 0, 0, time.UTC)

	if err := repo.StoreDocument(ctx, domain.MemoryDocument{
		ID:        "summary-cn-ui-1",
		NodeID:    "node-summary-cn-ui-1",
		UserID:    "user-1",
		Type:      domain.MemoryTypeSummary,
		Content:   "用户喜欢中文界面，并且希望默认使用浅色主题。",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("store summary: %v", err)
	}

	matches, err := repo.SearchKeywordMatches(ctx, "user-1", currentGenerationID("user-1"), "ui_theme: 浅色主题", 10)
	if err != nil {
		t.Fatalf("search keyword matches: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one lexical summary match, got %+v", matches)
	}
	if matches[0].NodeID != "node-summary-cn-ui-1" {
		t.Fatalf("expected chinese summary node, got %+v", matches[0])
	}
	if len(matches[0].Keywords) == 0 || matches[0].Keywords[0] != "浅色主题" {
		t.Fatalf("expected matched lexical keyword, got %+v", matches[0])
	}
}

func TestSegmentEntityPrefixPatternEscapesLikeWildcards(t *testing.T) {
	t.Parallel()

	got := segmentEntityPrefixPattern(`conv_%\demo`)
	want := `segment:conv\_\%\\demo:%`
	if got != want {
		t.Fatalf("expected escaped LIKE prefix %q, got %q", want, got)
	}
}

func TestInMemoryConversationRepositoryKeepsSegmentsIsolatedByGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryConversationRepository()
	userID := "user-1"
	conversationID := "conv-1"
	currentGenerationID := currentGenerationID(userID)
	candidateGenerationID := "gen:rebuild:user-1"
	now := time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC)

	if err := repo.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: conversationID,
		UserID:         userID,
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusCompleted,
		AnalysisStatus: domain.ProcessingStatusCompleted,
		Metadata: map[string]any{
			"memory_generation_id": currentGenerationID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}

	repo.segments[key(userID, conversationID)] = []domain.ConversationSegment{
		{
			ID:             candidateGenerationID + ":segment:" + conversationID + ":000",
			GenerationID:   candidateGenerationID,
			UserID:         userID,
			ConversationID: conversationID,
			SegmentIndex:   0,
			Content:        "candidate segment",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}

	if err := repo.ReplaceSegments(ctx, userID, conversationID, []domain.ConversationSegment{
		{
			ID:             "segment:" + conversationID + ":000",
			GenerationID:   currentGenerationID,
			UserID:         userID,
			ConversationID: conversationID,
			SegmentIndex:   0,
			Content:        "current segment",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("replace current generation segments: %v", err)
	}

	if len(repo.segments[key(userID, conversationID)]) != 2 {
		t.Fatalf("expected both current and candidate segments to be retained, got %d", len(repo.segments[key(userID, conversationID)]))
	}

	conversation, err := repo.GetConversation(ctx, userID, conversationID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(conversation.Segments) != 1 || conversation.Segments[0].GenerationID != currentGenerationID {
		t.Fatalf("expected current-generation segments only, got %+v", conversation.Segments)
	}

	if err := repo.UpdateConversationState(ctx, domain.ConversationStateUpdate{
		ConversationID: conversationID,
		UserID:         userID,
		Metadata: map[string]any{
			"memory_generation_id": candidateGenerationID,
		},
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("update conversation generation metadata: %v", err)
	}

	conversation, err = repo.GetConversation(ctx, userID, conversationID)
	if err != nil {
		t.Fatalf("get conversation after generation switch: %v", err)
	}
	if len(conversation.Segments) != 1 || conversation.Segments[0].GenerationID != candidateGenerationID {
		t.Fatalf("expected candidate-generation segments after switch, got %+v", conversation.Segments)
	}
}
