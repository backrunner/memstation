package analysis

import (
	"context"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestAnalyzeConversationBuildsFactImpressionAndRelations(t *testing.T) {
	t.Parallel()

	service := NewService()
	result, err := service.AnalyzeConversation(context.Background(), Input{
		UserID:         "user-1",
		ConversationID: "conv-1",
		Conversation: domain.ConversationRecord{
			ConversationID: "conv-1",
			UserID:         "user-1",
			Messages: []domain.UnifiedMessage{
				{Role: domain.RoleUser, Text: "Please help me design the MemStation server architecture."},
				{Role: domain.RoleAssistant, Text: "We should start from the gateway and memory pipeline."},
			},
		},
		Summary: domain.MemoryDocument{
			ID:      "summary:conv-1:v1",
			NodeID:  "node:summary:conv-1:v1",
			UserID:  "user-1",
			Type:    domain.MemoryTypeSummary,
			Content: "User asked to design the MemStation server architecture and implementation roadmap.",
		},
	})
	if err != nil {
		t.Fatalf("analyze conversation: %v", err)
	}

	if result.Fact.ID != FactDocumentID("conv-1") {
		t.Fatalf("unexpected fact id: %s", result.Fact.ID)
	}
	if !strings.Contains(strings.ToLower(result.Fact.Content), "memstation") {
		t.Fatalf("expected fact content to reference summary topic, got %q", result.Fact.Content)
	}
	if result.Impression.ID != ImpressionDocumentID("conv-1") {
		t.Fatalf("unexpected impression id: %s", result.Impression.ID)
	}
	if evaluation := result.Impression.Metadata["evaluation"]; evaluation != "planning" {
		t.Fatalf("expected planning evaluation, got %v", evaluation)
	}
	if len(result.Relations) != 3 {
		t.Fatalf("expected 3 relations, got %d", len(result.Relations))
	}
}

func TestRunFallbackExtractionHandlesEmptySummary(t *testing.T) {
	t.Parallel()

	service := NewService()
	result, err := service.RunFallbackExtraction(context.Background(), "")
	if err != nil {
		t.Fatalf("fallback extraction: %v", err)
	}
	if result.FactContent == "" {
		t.Fatal("expected fact content")
	}
	if result.ImpressionContent == "" {
		t.Fatal("expected impression content")
	}
	if _, ok := result.FactMetadata["confidence"]; !ok {
		t.Fatalf("expected confidence metadata, got %+v", result.FactMetadata)
	}
}

func TestSummarizeConversationAndHelpers(t *testing.T) {
	t.Parallel()

	summary := summarizeConversation([]domain.UnifiedMessage{
		{Role: domain.RoleSystem, Text: "ignore me"},
		{Role: domain.RoleUser, Text: "We hit an incident during rollout and need a rollback plan."},
		{Role: domain.RoleAssistant, Text: "Let's confirm the owner and retry safely."},
	})
	if !strings.Contains(strings.ToLower(summary), "incident") {
		t.Fatalf("expected summary to include user content, got %q", summary)
	}

	impression, metadata := inferImpression(summary)
	if !strings.Contains(strings.ToLower(impression), "problem-solving") {
		t.Fatalf("expected operational/problem-solving impression, got %q", impression)
	}
	if metadata["evaluation"] != "problem_solving" {
		t.Fatalf("unexpected evaluation metadata: %v", metadata["evaluation"])
	}

	if got := leadingSentence("First sentence. Second sentence."); got != "First sentence" {
		t.Fatalf("unexpected leading sentence: %q", got)
	}
	if got := truncate("abcdef", 4); got != "abcd..." {
		t.Fatalf("unexpected truncated string: %q", got)
	}
	if got := firstNonEmpty("", "alpha", "beta"); got != "alpha" {
		t.Fatalf("unexpected first non-empty value: %q", got)
	}
}
