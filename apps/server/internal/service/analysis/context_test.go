package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/memoryllm"
)

func TestKeyMemoryDoesNotReviveSupersededFacts(t *testing.T) {
	input := sourceInput()
	calls := 0
	s := NewService(generatorFunc(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"memories":[]}`), nil
	}))
	docs := []domain.MemoryDocument{{ID: "old", UserID: input.UserID, ConversationID: input.ConversationID, Type: domain.MemoryTypeFact, Content: "obsolete preference", Metadata: map[string]any{"is_superseded": true, "superseded_by_id": "new"}}}
	if _, err := s.ReconcileKeyMemories(context.Background(), input, docs, nil, []string{"preferences"}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("obsolete facts were offered to the model")
	}
}

func TestAnalysisBoundsExistingFactContext(t *testing.T) {
	input := sourceInput()
	for i := 0; i < 500; i++ {
		input.ExistingFacts = append(input.ExistingFacts, domain.MemoryDocument{ID: fmt.Sprint(i), UserID: "owner", Type: domain.MemoryTypeFact, Content: strings.Repeat("文", 1000)})
	}
	s := NewService(generatorFunc(func(_ context.Context, r memoryllm.Request) (json.RawMessage, error) {
		raw, _ := json.Marshal(r.Input)
		if len(raw) > 128*1024 {
			t.Fatal("existing facts exceed the model transport budget", len(raw))
		}
		return json.RawMessage(`{"facts":[],"impression":null,"items":[]}`), nil
	}))
	if _, err := s.AnalyzeConversation(context.Background(), input); err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisRelationsResolveKeysWithinEachChunk(t *testing.T) {
	input := sourceInput()
	input.Conversation.Messages = []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "任务甲" + strings.Repeat("文", 3000)}, {Role: domain.RoleUser, Text: "任务乙" + strings.Repeat("文", 3000)}}
	s := NewService(generatorFunc(func(_ context.Context, r memoryllm.Request) (json.RawMessage, error) {
		source := r.Input.(map[string]any)["sources"].([]Source)[0]
		text := source.Text[:9]
		item := Item{Content: text, Subject: "user", Importance: 1, DecayFactor: 1, Confidence: 1, Quotes: []Quote{{SourceID: source.ID, Text: text}}}
		return json.Marshal(Extraction{Facts: []Fact{{Item: item, Key: "draft", Category: "project"}}, Items: []GenericItem{{Item: item, Key: "draft_task", Type: domain.MemoryTypeTask, Title: text, Status: "planned"}}, Relations: []Relation{{SourceKey: "draft", TargetKey: "draft_task", Type: domain.MemoryRelationSupports, Weight: 1}}})
	}))
	result, err := s.AnalyzeConversation(context.Background(), input)
	if err != nil || len(result.Facts) != 2 || len(result.GenericItems) != 2 {
		t.Fatal("different chunks reused local keys incorrectly", result, err)
	}
	for _, relation := range result.Relations {
		if relation.RelationType != domain.MemoryRelationSupports {
			continue
		}
		found := false
		for i, fact := range result.Facts {
			if relation.SrcNodeID == fact.NodeID && relation.DstNodeID == result.GenericItems[i].NodeID {
				found = true
			}
		}
		if !found {
			t.Fatal("chunk relation crossed into another chunk", relation)
		}
	}
}
