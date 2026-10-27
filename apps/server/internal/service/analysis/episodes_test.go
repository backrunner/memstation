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

func episodeFixture() SemanticEpisode {
	return SemanticEpisode{Key: "museum_visit", Type: domain.MemoryTypeEpisode, Item: Item{Subject: "Alice", Content: "Alice cancelled the planned Friday museum visit after the closure notice; no visit took place.", Importance: 1, DecayFactor: 1, Confidence: 1, Quotes: []Quote{{SourceID: "message-1", Text: "Alice cancelled the Friday museum visit. It did not take place."}}}, Title: "Cancelled museum visit", Status: "cancelled", Participants: []string{"Alice"}, TimeAnchor: &Quote{SourceID: "message-1", Text: "Friday"}}
}

func TestSemanticEpisodeOptInAndValidatedNarrative(t *testing.T) {
	input := Input{UserID: "u", ConversationID: "c", Conversation: domain.ConversationRecord{UserID: "u", ConversationID: "c", Revision: 3, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "Alice cancelled the Friday museum visit. It did not take place."}}, AgentID: "fixture-agent"}}
	generator := generatorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
		if !strings.Contains(req.Instruction, "Additionally return episodes") {
			return json.RawMessage(`{"facts":[],"impression":null,"items":[]}`), nil
		}
		return json.Marshal(Extraction{Facts: []Fact{}, Items: []GenericItem{}, Episodes: []SemanticEpisode{episodeFixture()}})
	})
	off, err := NewService(generator).AnalyzeConversation(context.Background(), input)
	if err != nil || len(off.GenericItems) != 0 {
		t.Fatal("default enabled episodes", err)
	}
	svc := NewServiceWithOptions(generator, Options{SemanticEpisodes: true})
	on, err := svc.AnalyzeConversation(context.Background(), input)
	if err != nil || len(on.GenericItems) != 1 || len(on.Facts) != 0 {
		t.Fatal(on, err)
	}
	doc := on.GenericItems[0]
	if doc.Type != domain.MemoryTypeEpisode || doc.Metadata["status"] != "cancelled" || doc.Metadata["source_conversation_revision"] != int64(3) || doc.Metadata["semantic_unit_version"] != "episode_v1" {
		t.Fatal(doc)
	}
	input.Conversation.Metadata = map[string]any{"source_session_time": "9:00 am on 23 October, 2026"}
	dated, err := svc.AnalyzeConversation(context.Background(), input)
	if err != nil || dated.GenericItems[0].Metadata["source_session_time"] != "9:00 am on 23 October, 2026" {
		t.Fatal("explicit source clock lost", err)
	}
	again, err := svc.AnalyzeConversation(context.Background(), input)
	if err != nil || again.GenericItems[0].ID != doc.ID {
		t.Fatal("unstable validated episode identity", err)
	}
}

func TestSemanticEpisodeRejectsUnsupportedFields(t *testing.T) {
	sources := []Source{{ID: "message-1", Role: domain.RoleUser, Text: "Alice cancelled the Friday museum visit. It did not take place."}}
	for _, mutation := range []func(*SemanticEpisode){
		func(e *SemanticEpisode) { e.Status = "fictional" },
		func(e *SemanticEpisode) { e.Participants = []string{"Bob"} },
		func(e *SemanticEpisode) { e.Participants = nil },
		func(e *SemanticEpisode) { e.TimeAnchor = &Quote{SourceID: "foreign", Text: "Friday"} },
		func(e *SemanticEpisode) { e.TimeAnchor = &Quote{SourceID: "message-1", Text: "May 2026"} },
		func(e *SemanticEpisode) { e.Quotes = []Quote{{SourceID: "message-1", Text: "Alice attended"}} },
	} {
		e := episodeFixture()
		mutation(&e)
		if _, err := episodeDocument(e, "u", "c", sources); err == nil {
			t.Fatal("accepted ungrounded episode", e)
		}
	}
	unknown := sources
	unknown[0].Role = domain.RoleAssistant
	if _, err := episodeDocument(episodeFixture(), "u", "c", unknown); err == nil {
		t.Fatal("assistant suggestion became evidence")
	}
}

func TestEpisodeQuoteUsesOriginalByteAndRuneOffsetsAfterSplit(t *testing.T) {
	text := strings.Repeat("界", 4000) + "Alice cancelled the Friday museum visit. It did not take place."
	ref := &domain.SourceRef{WorkspaceID: "w", MessageID: "message", Revision: 2}
	batches, err := sourceBatches([]Source{{ID: "message-1", Role: domain.RoleUser, Text: text, Ref: ref, AuthorActorID: "actor", SourceTime: "2026-10-23T08:00:00Z"}})
	if err != nil {
		t.Fatal(err)
	}
	// Both chunks fit one 16KB batch and deliberately share the source ID.
	e := episodeFixture()
	e.SubjectScope = "actor"
	e.SubjectActorIDs = []string{"actor"}
	doc, err := episodeDocument(e, "u", "c", batches[0])
	if err != nil || len(doc.SourceRefs) != 2 {
		t.Fatal(doc, err)
	}
	quote := doc.Metadata["source_quotes"].([]any)[0].(map[string]any)
	if quote["start_byte"] != 12000 || doc.SourceRefs[0].Span.Start != 4000 || doc.Metadata["source_times"].(map[string]string)["message-1"] == "" {
		t.Fatal("split lost original offsets or time", doc)
	}
}

func TestDisabledEpisodesAndMissingEnabledCollectionFail(t *testing.T) {
	in := sourceInput()
	raw, _ := json.Marshal(Extraction{Facts: []Fact{}, Items: []GenericItem{}, Episodes: []SemanticEpisode{episodeFixture()}})
	if _, err := withBody(raw).AnalyzeConversation(context.Background(), in); err == nil {
		t.Fatal("disabled episode admitted")
	}
	svc := NewServiceWithOptions(generatorFunc(func(context.Context, memoryllm.Request) (json.RawMessage, error) {
		return json.RawMessage(`{"facts":[],"items":[],"impression":null}`), nil
	}), Options{SemanticEpisodes: true})
	if _, err := svc.AnalyzeConversation(context.Background(), in); err == nil {
		t.Fatal("missing enabled episode collection silently ignored")
	}
}

func TestEpisodeIdentityKeepsTrustedSubjectsSeparateAndCopiesTimeAnchor(t *testing.T) {
	source := Source{ID: "message-1", Role: domain.RoleUser, Text: "Alice cancelled the Friday museum visit. It did not take place.", Ref: &domain.SourceRef{WorkspaceID: "w", MessageID: "source", Revision: 1}, AuthorActorID: "actor-a", MentionActorIDs: []string{"actor-b"}}
	e := episodeFixture()
	e.SubjectScope = "actor"
	e.SubjectActorIDs = []string{"actor-a"}
	a, err := episodeDocument(e, "u", "c", []Source{source})
	if err != nil {
		t.Fatal(err)
	}
	e.SubjectActorIDs = []string{"actor-b"}
	b, err := episodeDocument(e, "u", "c", []Source{source})
	if err != nil || a.ID == b.ID {
		t.Fatal("distinct actor subjects collapsed", err)
	}
	e.TimeAnchor.Text = "changed caller object"
	if a.Metadata["time_anchor"].(map[string]any)["text"] != "Friday" {
		t.Fatal("caller mutated stored time anchor")
	}
}

func TestQuotedSourceSequencesDistinguishPartsAndFallbackMessages(t *testing.T) {
	text := "Alice cancelled the Friday museum visit. It did not take place."
	for _, record := range []domain.ConversationRecord{
		{Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: text}}},
		{Parts: []domain.MessagePart{{PartID: "message-1", Role: domain.RoleUser, SequenceNum: 0, TextContent: text}}},
	} {
		sources := Sources(record)
		doc, err := episodeDocument(episodeFixture(), "u", "c", sources)
		if err != nil {
			t.Fatal(err)
		}
		quote := doc.Metadata["source_quotes"].([]any)[0].(map[string]any)
		if quote["sequence_num"].(int)-quote["sequence_base"].(int) != 0 {
			t.Fatal("source quote did not address first message", quote)
		}
	}
}

func TestEpisodeBatchesBoundShortTurnsWithoutDroppingSources(t *testing.T) {
	in := Input{UserID: "u", ConversationID: "c", Conversation: domain.ConversationRecord{UserID: "u", ConversationID: "c", AgentID: "fixture-agent"}}
	for i := range 39 {
		in.Conversation.Messages = append(in.Conversation.Messages, domain.UnifiedMessage{Role: domain.RoleUser, Text: fmt.Sprintf("Turn %d: %s", i, strings.Repeat("界", 65))})
	}
	for _, enabled := range []bool{false, true} {
		calls, sourceCount := 0, 0
		gotText := ""
		g := generatorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
			batch := req.Input.(map[string]any)["sources"].([]Source)
			calls++
			size := 0
			for _, s := range batch {
				size += len(s.Text)
				gotText += s.Text
			}
			if enabled && (len(batch) > episodeBatchSources || size > episodeBatchBytes) {
				t.Fatal("unbounded episode batch", len(batch), size)
			}
			sourceCount += len(batch)
			return json.RawMessage(`{"facts":[],"items":[],"impression":null,"episodes":[]}`), nil
		})
		_, err := NewServiceWithOptions(g, Options{SemanticEpisodes: enabled}).AnalyzeConversation(context.Background(), in)
		wantCalls := 1
		if enabled {
			wantCalls = 4
		}
		wantText := ""
		for _, s := range Sources(in.Conversation) {
			wantText += s.Text
		}
		if err != nil || calls != wantCalls || sourceCount != 39 || gotText != wantText {
			t.Fatal("batching lost/reordered sources or changed legacy calls", enabled, calls, sourceCount, err)
		}
	}
}

func TestEpisodeLongSourceBatchesKeepExactByteAndRuneSpans(t *testing.T) {
	text := strings.Repeat("记忆", 2500) + "tail"
	batches, err := sourceBatchesWithLimits([]Source{{ID: "source", Text: text, Ref: &domain.SourceRef{WorkspaceID: "w", MessageID: "m", Revision: 1}}}, episodeBatchBytes, episodeBatchBytes, episodeBatchSources)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, batch := range batches {
		for _, s := range batch {
			if len(s.Text) > episodeBatchBytes || s.ByteOffset != len(joined) || string([]rune(text)[s.Ref.Span.Start:s.Ref.Span.End]) != s.Text {
				t.Fatal("split lost original position", s)
			}
			joined += s.Text
		}
	}
	if joined != text {
		t.Fatal("source truncated")
	}
}
