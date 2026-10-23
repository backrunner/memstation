package analysis

import (
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestGroundingRequiresExactUserOrToolSpan(t *testing.T) {
	conv := domain.ConversationRecord{UserID: "u", ConversationID: "c", Parts: []domain.MessagePart{{PartID: "p", SequenceNum: 1, Role: domain.RoleUser, TextContent: "I now live in Shanghai. I prefer tea."}, {PartID: "assistant", SequenceNum: 2, Role: domain.RoleAssistant, TextContent: "You own a house in Shanghai."}}}
	d := domain.MemoryDocument{ID: "fact", NodeID: "n", UserID: "u", ConversationID: "c", Type: domain.MemoryTypeFact, Content: "Current home location: I now live in Shanghai."}
	grounded := GroundDocument(d, conv)
	quotes := grounded.Metadata["source_quotes"].([]any)
	if len(quotes) != 1 || grounded.Metadata["support_kind"] != "user_statement" {
		t.Fatalf("missing exact source: %+v", grounded)
	}
	q := quotes[0].(map[string]any)
	if q["quote"] != "I now live in Shanghai." || q["start_byte"] != 0 || q["end_byte"] != 23 {
		t.Fatalf("incorrect source span: %+v", q)
	}
	d.ID = "another"
	d.NodeID = "other"
	again := GroundDocument(d, conv)
	if again.Metadata["source_quotes"].([]any)[0].(map[string]any)["source_id"] != q["source_id"] {
		t.Fatal("same source counted twice")
	}
	d.Content = "You own a house in Shanghai."
	inferred := GroundDocument(d, conv)
	if len(inferred.Metadata["source_quotes"].([]any)) != 0 || inferred.Metadata["support_kind"] != "model_inference" {
		t.Fatal("assistant assertion promoted to user evidence")
	}
	d.Content = "Current home location: I now live in Beijing."
	if len(GroundDocument(d, conv).Metadata["source_quotes"].([]any)) != 0 {
		t.Fatal("unsupported claim grounded")
	}
}

func TestCanonicalEntityScopeSeparatesHomonyms(t *testing.T) {
	a := canonicalEntityKey("Entity: Atlas", map[string]any{"entity_scope": "Work"})
	b := canonicalEntityKey("Entity: Atlas", map[string]any{"entity_scope": "Personal"})
	if a == b {
		t.Fatal("same-name projects merged across scope")
	}
}
