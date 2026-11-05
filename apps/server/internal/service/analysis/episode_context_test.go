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

func TestEpisodeOverlapPreservesOriginalCoordinatesAndBudgets(t *testing.T) {
	text := strings.Repeat("边界记忆🙂", 900)
	source := Source{ID: "source", Role: domain.RoleUser, Text: text, Ref: &domain.SourceRef{WorkspaceID: "w", MessageID: "message", Revision: 3}}
	batches, err := episodeSourceBatches([]Source{source})
	if err != nil || len(batches) < 2 {
		t.Fatal(err)
	}
	joined := ""
	for index, batch := range batches {
		size, contextSize, contextCount := 0, 0, 0
		ids := map[string]bool{}
		for _, part := range batch {
			size += len(part.Text)
			if ids[part.ID] {
				t.Fatal("ambiguous source-window identity")
			}
			ids[part.ID] = true
			if string([]rune(text)[part.Ref.Span.Start:part.Ref.Span.End]) != part.Text || text[part.ByteOffset:part.ByteOffset+len(part.Text)] != part.Text {
				t.Fatal("source coordinates changed")
			}
			if part.ContextOnly {
				contextSize += len(part.Text)
				contextCount++
			} else {
				joined += part.Text
			}
		}
		if size > episodeBatchBytes || len(batch) > episodeBatchSources || contextSize > episodeContextBytes || contextCount > episodeContextSources || (index > 0 && contextCount == 0) {
			t.Fatal("unbounded or missing overlap")
		}
	}
	if joined != text || source.Ref.Span != nil {
		t.Fatal("primary content dropped, repeated or mutated")
	}
}

func TestEpisodeCrossBatchAntecedentRequiresNewEvidence(t *testing.T) {
	input := Input{UserID: "u", ConversationID: "c", Conversation: domain.ConversationRecord{UserID: "u", ConversationID: "c", AgentID: "agent"}}
	for i := range 9 {
		input.Conversation.Messages = append(input.Conversation.Messages, domain.UnifiedMessage{Role: domain.RoleUser, Text: fmt.Sprintf("Unrelated original turn %d.", i)})
	}
	input.Conversation.Messages = append(input.Conversation.Messages,
		domain.UnifiedMessage{Role: domain.RoleUser, Text: "Alex plans a museum visit on Friday."},
		domain.UnifiedMessage{Role: domain.RoleUser, Text: "That visit was cancelled; it did not happen."})
	calls := 0
	g := generatorFunc(func(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
		calls++
		batch := req.Input.(map[string]any)["sources"].([]Source)
		x := Extraction{Facts: []Fact{}, Items: []GenericItem{}, Episodes: []SemanticEpisode{}}
		if calls == 1 {
			return json.Marshal(x)
		}
		var antecedent, update Source
		for _, source := range batch {
			if source.ContextOnly && strings.Contains(source.Text, "Alex plans") {
				antecedent = source
			}
			if !source.ContextOnly && strings.Contains(source.Text, "That visit") {
				update = source
			}
		}
		if antecedent.ID == "" || update.ID == "" || !strings.Contains(req.Instruction, episodeContextInstruction) {
			t.Fatal("missing original antecedent or context contract")
		}
		e := episodeFixture()
		e.Content, e.Subject, e.Participants = "Alex's planned museum visit was cancelled, not completed.", "Alex", []string{"Alex"}
		e.TimeAnchor = nil
		e.Quotes = []Quote{{SourceID: antecedent.ID, Text: antecedent.Text}}
		if _, err := episodeDocument(e, "u", "c", batch); err == nil {
			t.Fatal("old-only context re-emitted as a new event")
		}
		e.Quotes = append(e.Quotes, Quote{SourceID: update.ID, Text: update.Text})
		x.Episodes = []SemanticEpisode{e}
		return json.Marshal(x)
	})
	result, err := NewServiceWithOptions(g, Options{SemanticEpisodes: true}).AnalyzeConversation(context.Background(), input)
	if err != nil || calls != 2 || len(result.GenericItems) != 1 {
		t.Fatal(result, calls, err)
	}
	doc := result.GenericItems[0]
	seq := domain.SourceQuoteMessageSequences(doc.Metadata)
	if len(seq) != 2 || seq[0] != 9 || seq[1] != 10 || doc.Metadata["status"] != "cancelled" {
		t.Fatal("antecedent/new observation provenance lost", seq, doc.Metadata)
	}
}

func TestEpisodeOverlapDoesNotReplayAttachments(t *testing.T) {
	sources := make([]Source, 11)
	for i := range sources {
		sources[i] = Source{ID: fmt.Sprint(i), Text: "Original turn", Role: domain.RoleUser}
	}
	sources[9].MediaSHA256 = "attachment"
	sources[9].NativeMedia = true
	batches, err := episodeSourceBatches(sources)
	if err != nil || len(batches) != 2 || len(batches[1]) != 1 {
		t.Fatal("overlap replayed or jumped over attachment", err)
	}
}

func TestEpisodeContextCannotCollideWithAnOriginalSourceID(t *testing.T) {
	sources := make([]Source, 11)
	for i := range sources {
		sources[i] = Source{ID: fmt.Sprint(i), Text: "Original turn", Role: domain.RoleUser}
	}
	sources[10].ID = "context-window-1-9"
	batches, err := episodeSourceBatches(sources)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, source := range batches[1] {
		if seen[source.ID] {
			t.Fatal("context shadowed original evidence")
		}
		seen[source.ID] = true
	}
}
