package analysis

import (
	"context"
	"fmt"
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

	if !strings.HasPrefix(result.Fact.ID, "fact:conv_1:") {
		t.Fatalf("unexpected fact id: %s", result.Fact.ID)
	}
	if result.Fact.Metadata["fact_key"] == "" {
		t.Fatalf("expected fact key metadata, got %+v", result.Fact.Metadata)
	}
	if result.Fact.Metadata["lineage_group_id"] == "" {
		t.Fatalf("expected lineage group metadata, got %+v", result.Fact.Metadata)
	}
	if !strings.HasPrefix(result.Fact.Metadata["lineage_group_id"].(string), "fact-lineage:user-1:") {
		t.Fatalf("expected unsanitized user id in lineage group, got %+v", result.Fact.Metadata)
	}
	if result.Fact.Metadata["lineage_revision_key"] == "" {
		t.Fatalf("expected lineage revision metadata, got %+v", result.Fact.Metadata)
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
	if len(result.Relations) < 3 {
		t.Fatalf("expected at least summary/fact/impression relations, got %d", len(result.Relations))
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

func TestAnalyzeConversationMarksConflictingLocationFact(t *testing.T) {
	t.Parallel()

	service := NewService()
	result, err := service.AnalyzeConversation(context.Background(), Input{
		UserID:         "user-1",
		ConversationID: "conv-new",
		Summary: domain.MemoryDocument{
			ID:      "summary:conv-new:v1",
			NodeID:  "node:summary:conv-new:v1",
			UserID:  "user-1",
			Type:    domain.MemoryTypeSummary,
			Content: "The user moved and now lives in Shanghai.",
		},
		ExistingFacts: []domain.MemoryDocument{{
			ID:      "fact-old-location",
			UserID:  "user-1",
			Type:    domain.MemoryTypeFact,
			Content: "The user lives in Hangzhou.",
			Metadata: map[string]any{
				"category": "location",
				"subject":  "user",
				"fact_key": "home_location",
			},
		}},
	})
	if err != nil {
		t.Fatalf("analyze conversation: %v", err)
	}
	if result.Fact.Metadata["category"] != "location" {
		t.Fatalf("expected location category, got %+v", result.Fact.Metadata)
	}
	if result.Fact.Metadata["lineage_group_id"] == "" {
		t.Fatalf("expected lineage group metadata, got %+v", result.Fact.Metadata)
	}
	ids, ok := result.Fact.Metadata["supersedes_fact_ids"].([]string)
	if !ok || len(ids) != 1 || ids[0] != "fact-old-location" {
		t.Fatalf("expected old location fact to be superseded, got %+v", result.Fact.Metadata)
	}
}

func TestAnalyzeConversationExtractsMultipleStructuredFacts(t *testing.T) {
	t.Parallel()

	service := NewService()
	result, err := service.AnalyzeConversation(context.Background(), Input{
		UserID:         "user-1",
		ConversationID: "conv-preference-plan",
		Summary: domain.MemoryDocument{
			ID:      "summary:conv-preference-plan:v1",
			NodeID:  "node:summary:conv-preference-plan:v1",
			UserID:  "user-1",
			Type:    domain.MemoryTypeSummary,
			Content: "The user prefers a light UI theme and plans to move to Shanghai next month.",
		},
	})
	if err != nil {
		t.Fatalf("analyze conversation: %v", err)
	}
	if len(result.Facts) < 2 {
		t.Fatalf("expected multiple structured facts, got %+v", result.Facts)
	}
	keys := map[string]struct{}{}
	for _, fact := range result.Facts {
		key, _ := fact.Metadata["fact_key"].(string)
		keys[key] = struct{}{}
		if fact.Metadata["lineage_group_id"] == "" || fact.Metadata["lineage_revision_key"] == "" {
			t.Fatalf("expected lineage metadata on fact %+v", fact.Metadata)
		}
	}
	if _, ok := keys["preference_ui_theme"]; !ok {
		t.Fatalf("expected preference fact, got keys %+v", keys)
	}
	if _, ok := keys["plan_relocation"]; !ok {
		t.Fatalf("expected plan fact, got keys %+v", keys)
	}
	minRelations := len(result.Facts)*2 + 1
	if len(result.Relations) < minRelations {
		t.Fatalf("expected summary/fact/impression relations for all facts, got %d relations for %d facts", len(result.Relations), len(result.Facts))
	}
}

func TestAnalyzeConversationKeepsDistinctFactKeysForUnrelatedPreferenceAndPlans(t *testing.T) {
	t.Parallel()

	service := NewService()
	result, err := service.AnalyzeConversation(context.Background(), Input{
		UserID:         "user-1",
		ConversationID: "conv-distinct-facts",
		Summary: domain.MemoryDocument{
			ID:      "summary:conv-distinct-facts:v1",
			NodeID:  "node:summary:conv-distinct-facts:v1",
			UserID:  "user-1",
			Type:    domain.MemoryTypeSummary,
			Content: "The user prefers a compact UI layout. The user plans to renew their passport. The gateway service deadline is next week.",
		},
	})
	if err != nil {
		t.Fatalf("analyze conversation: %v", err)
	}
	keys := map[string]struct{}{}
	for _, fact := range result.Facts {
		if key, _ := fact.Metadata["fact_key"].(string); key != "" {
			keys[key] = struct{}{}
		}
	}
	if _, ok := keys["preference_ui_layout"]; !ok {
		t.Fatalf("expected layout preference key, got %+v", keys)
	}
	if _, ok := keys["user_plan_passport"]; !ok {
		t.Fatalf("expected scoped passport plan key, got %+v", keys)
	}
	if _, ok := keys["work_schedule_gateway"]; !ok {
		t.Fatalf("expected scoped gateway schedule key, got %+v", keys)
	}
}

func TestAnalyzeConversationExtractsGenericAgentMemoryItems(t *testing.T) {
	t.Parallel()

	service := NewService()
	result, err := service.AnalyzeConversation(context.Background(), Input{
		UserID:         "user-1",
		ConversationID: "conv-agent",
		Conversation: domain.ConversationRecord{
			ConversationID: "conv-agent",
			UserID:         "user-1",
			Messages: []domain.UnifiedMessage{{
				Role: domain.RoleAssistant,
				ToolCalls: []domain.ToolCall{{
					ID:   "call-1",
					Name: "inspect_assets",
					Arguments: map[string]any{
						"path": "/tmp/report.pdf",
					},
				}},
			}, {
				Role: domain.RoleTool,
				ToolResults: []domain.ToolResult{{
					ToolCallID: "call-1",
					Name:       "inspect_assets",
					Output: map[string]any{
						"status":     "sk-test-raw-payload",
						"result":     "sk-result-raw-payload",
						"error_type": "sk-error-raw-payload",
						"pages":      3,
					},
				}},
			}},
		},
		Summary: domain.MemoryDocument{
			ID:      "summary:conv-agent:v1",
			NodeID:  "node:summary:conv-agent:v1",
			UserID:  "user-1",
			Type:    domain.MemoryTypeSummary,
			Content: "The user plans to review the report next week. The workflow should use inspect_assets for PDF inspection.",
		},
	})
	if err != nil {
		t.Fatalf("analyze conversation: %v", err)
	}
	types := map[domain.MemoryType]int{}
	for _, item := range result.GenericItems {
		types[item.Type]++
		if item.ID == "" || item.NodeID == "" {
			t.Fatalf("expected stable IDs for generic item: %+v", item)
		}
		if item.Type == domain.MemoryTypeAgentAction {
			serializedMetadata := fmt.Sprint(item.Metadata)
			for _, secret := range []string{"sk-test-raw-payload", "sk-result-raw-payload", "sk-error-raw-payload"} {
				if strings.Contains(item.Content, secret) || strings.Contains(serializedMetadata, secret) {
					t.Fatalf("agent action should not retain raw tool output payload %q: %+v", secret, item)
				}
			}
			if strings.Contains(metadataString(item.Metadata, "result_summary"), "status=sk") {
				t.Fatalf("agent action should not retain raw tool output payload: %+v", item)
			}
		}
	}
	for _, kind := range []domain.MemoryType{domain.MemoryTypeTask, domain.MemoryTypeProcedure, domain.MemoryTypeAgentAction, domain.MemoryTypeEntity} {
		if types[kind] == 0 {
			t.Fatalf("expected generic memory type %s, got counts %+v", kind, types)
		}
	}
	foundToolRelation := false
	for _, relation := range result.Relations {
		if relation.RelationType == domain.MemoryRelationUsedTool {
			foundToolRelation = true
			break
		}
	}
	if !foundToolRelation {
		t.Fatalf("expected used_tool relation, got %+v", result.Relations)
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
