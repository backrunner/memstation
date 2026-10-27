package conversation

import (
	"context"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"
)

func TestInMemoryConversationRepositoryKeepsSegmentsIsolatedByGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryConversationRepository()
	userID := "user-1"
	conversationID := "conv-1"
	currentGenerationID := recordutilstore.CurrentGenerationID(domain.PersonalWorkspaceID(userID))
	candidateGenerationID := "gen:rebuild:user-1"
	now := time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC)

	if err := repo.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: conversationID,
		UserID:         userID,
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAICompatible,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusCompleted,
		AnalysisStatus: domain.ProcessingStatusCompleted,
		Metadata: map[string]any{
			"memory_generation_id": currentGenerationID,
		},
		CreatedAt: now,
		UpdatedAt: now, AgentID: "fixture-agent",
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}

	repo.segments[recordutilstore.Key(userID, conversationID)] = []domain.ConversationSegment{
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

	if len(repo.segments[recordutilstore.Key(userID, conversationID)]) != 2 {
		t.Fatalf("expected both current and candidate segments to be retained, got %d", len(repo.segments[recordutilstore.Key(userID, conversationID)]))
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
