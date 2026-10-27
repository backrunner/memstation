package memory

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
)

func coreFixture(ids ...string) []domain.MemoryDocument {
	docs := []domain.MemoryDocument{}
	for _, id := range ids {
		docs = append(docs, domain.MemoryDocument{ID: id, UserID: "owner", WorkspaceID: domain.PersonalWorkspaceID("owner"), GenerationID: ("gen:current:" + domain.PersonalWorkspaceID("owner")), Type: domain.MemoryTypeFact, Content: id})
	}
	return docs
}

func TestEvidencePacketProtectsCoreBeforeSourceReserve(t *testing.T) {
	base := coreFixture("ordinary", "required")
	base[0].Content = strings.Repeat("other ", 80)
	base[1].Content = strings.Repeat("essential ", 80)
	sources := coreFixture("source")
	sources[0].Content = strings.Repeat("neighbor ", 80)
	budget := len(buildEvidenceSystemPrompt(base[1:]))
	got := packEvidence(base, sources, EvidenceOptions{MaxBytes: budget, RequiredIDs: []string{"required"}})
	if len(got) != 1 || got[0].ID != "required" || len(buildEvidenceSystemPrompt(got)) > budget {
		t.Fatal("source reserve displaced core")
	}
	packet, err := NewEngine(nil, nil).PrepareEvidence(context.Background(), domain.SearchInput{UserID: "owner"}, base, EvidenceOptions{MaxBytes: budget - 1, RequiredIDs: []string{"required"}})
	if err != nil || packet.Diagnostics.Core == nil || !reflect.DeepEqual(packet.Diagnostics.Core.OmittedIDs, []string{"required"}) || len(packet.Text) > budget-1 {
		t.Fatal(packet.Diagnostics, err)
	}
}

func TestEvidencePacketCoreRespectsTokenLimitAndTurnDedup(t *testing.T) {
	docs := coreFixture("first", "duplicate")
	for i := range docs {
		docs[i].ConversationID = "conversation"
		docs[i].Content = "same original turn"
		docs[i].Metadata = map[string]any{"source_turn": true, "start_sequence": 2, "end_sequence": 2}
	}
	engine := NewEngine(nil, nil)
	options := EvidenceOptions{RequiredIDs: []string{"first", "duplicate"}, MaxBytes: 16000}
	packet, err := engine.PrepareEvidence(context.Background(), domain.SearchInput{UserID: "owner"}, docs, options)
	if err != nil || len(packet.Memories) != 1 || !reflect.DeepEqual(packet.Diagnostics.Core.OmittedIDs, []string{"duplicate"}) {
		t.Fatal(packet, err)
	}
	options.MaxEstimatedTokens = 1
	packet, err = engine.PrepareEvidence(context.Background(), domain.SearchInput{UserID: "owner"}, docs, options)
	if err != nil || len(packet.Memories) != 0 || len(packet.Diagnostics.Core.OmittedIDs) != 2 {
		t.Fatal("protection exceeded token budget", packet, err)
	}
}

func TestSourceQuoteSequenceBasesDoNotHydratePreviousMessage(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want []int
	}{
		{`[{"sequence_num":0,"sequence_base":0}]`, []int{0}},
		{`[{"sequence_num":1,"sequence_base":0}]`, []int{1}},
		{`[{"sequence_num":1,"sequence_base":1}]`, []int{0}},
		{`[{"sequence_num":1}]`, nil},
		{`[{"sequence_num":0,"part_id":"p0"}]`, nil},
		{`[{"sequence_num":1,"part_id":"p1"}]`, nil},
		{`[{"sequence_num":0,"part_id":"p0","artifact_id":"a"}]`, nil},
		{`[{"sequence_num":1,"sequence_base":0,"part_id":"p1","artifact_id":"a"}]`, []int{1}},
		{`[{"sequence_num":0,"sequence_base":-1}]`, nil},
		{`[{"sequence_base":0}]`, nil},
	} {
		var quotes any
		if err := json.Unmarshal([]byte(test.raw), &quotes); err != nil {
			t.Fatal(err)
		}
		doc := domain.MemoryDocument{Metadata: map[string]any{"source_quotes": quotes}}
		if got := sourceAnchorSequences(doc); !reflect.DeepEqual(got, test.want) {
			t.Fatal("wrong source message hydrated", test.raw, got, test.want)
		}
	}
}
