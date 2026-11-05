package memory

import (
	"context"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
)

// Synthetic product contracts distilled from evidence-loss failure classes.
// No benchmark person, question, identifier or answer is used here.
func TestEvidenceKeepsRankedWholeRecordBeforeShortDistractors(t *testing.T) {
	docs := coreFixture("ranked-first", "short-a", "short-b")
	docs[0].Content = strings.Repeat("The field notebook records equipment maintenance. ", 20) + "The final inspection was postponed, not completed."
	docs[1].Content = "Equipment was inspected."
	docs[2].Content = "The notebook is short."
	engine := NewEngine(nil, nil)
	input := domain.SearchInput{UserID: "owner", Query: "Was the final inspection completed?"}
	whole, err := engine.PrepareEvidence(context.Background(), input, docs[:1], EvidenceOptions{MaxBytes: 16000})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := engine.PrepareEvidence(context.Background(), input, docs, EvidenceOptions{MaxBytes: len(whole.Text)})
	if err != nil || len(packet.Memories) != 1 || packet.Memories[0].ID != docs[0].ID || packet.Memories[0].Content != docs[0].Content || packet.Text != whole.Text || packet.SourceIDs["memory-1"] != docs[0].ID {
		t.Fatal("a short record displaced ranked evidence or removed its qualifying tail", packet, err)
	}
}

func TestEvidenceDoesNotReplaceOriginalDateOrDropComplementarySameSourceFact(t *testing.T) {
	docs := coreFixture("schedule", "qualification")
	docs[0].Content = "The exhibit opens on 14 May. Last Tuesday I only booked the room."
	docs[1].Content = "The room booking was later cancelled; the exhibit has no confirmed venue."
	for i := range docs {
		docs[i].ConversationID = "source-conversation"
		docs[i].SourceRefs = []domain.SourceRef{{WorkspaceID: docs[i].WorkspaceID, MessageID: "original-turn", Revision: 1}}
	}
	packet, err := NewEngine(nil, nil).PrepareEvidence(context.Background(), domain.SearchInput{UserID: "owner", Query: "When and where is the exhibit?"}, docs, EvidenceOptions{MaxBytes: 16000})
	if err != nil || len(packet.Memories) != 2 {
		t.Fatal("complementary observations sharing a citation were collapsed", packet, err)
	}
	for i, original := range docs {
		if packet.Memories[i].Content != original.Content || !strings.Contains(packet.Text, original.Content) {
			t.Fatal("literal date, relative date, or qualification rewritten")
		}
	}
}
