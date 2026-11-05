package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	memorystore "memstation/apps/server/internal/store/sql/memory"
)

type provenanceEvidenceReader struct{ calls map[string]int }

func (r *provenanceEvidenceReader) ReadSourceWindow(_ context.Context, in domain.SourceWindowInput) ([]domain.SourceTurn, error) {
	r.calls[in.ConversationID]++
	if in.ConversationRevision != 1 || len(in.Sequences) != 1 {
		return nil, fmt.Errorf("source coordinates changed")
	}
	return []domain.SourceTurn{{
		UserID: in.UserID, WorkspaceID: in.WorkspaceID, ConversationID: in.ConversationID,
		Sequence: in.Sequences[0], Text: "Harbor", Anchor: true,
		Speaker: in.ConversationID + " speaker", OccurredAt: time.Date(2026, 1, in.Sequences[0]+1, 12, 0, 0, 0, time.UTC),
	}}, nil
}

// Identical inference text must not erase separate source paths. This checks
// the complete search-to-hydration chain, without prescribing how many model
// requests an implementation uses to score repeated text.
func TestRerankPreservesIdenticalMentionSourceIdentities(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} {
		doc := domain.MemoryDocument{
			ID: id, NodeID: id, UserID: "owner", Type: domain.MemoryTypeEntity, Content: "Harbor",
			ConversationID: "conversation-" + id, Importance: 1, DecayFactor: 1, CreatedAt: now, UpdatedAt: now,
			Metadata: map[string]any{
				"extraction_method": "lightweight", "interpretation": "source_mention", "source_conversation_revision": 1,
				"source_quotes": []map[string]any{{"sequence_num": i + 1, "sequence_base": 1, "quote": "Harbor", "start_byte": 0, "end_byte": 6}},
			},
		}
		if id == "c" {
			doc.Type, doc.Content, doc.Metadata = domain.MemoryTypeFact, "The bicycle inspection was postponed.", nil
		}
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	e := NewEngine(repo, unboundedRerankVectors{})
	e.SetClock(func() time.Time { return now })
	e.SetRerankConfig(RerankConfig{Model: "test:model", MinCandidates: 3, MaxCandidates: 3})
	e.SetReranker(rerankFunc(func(_ context.Context, in domain.RerankInput) ([]domain.RerankResult, error) {
		var out []domain.RerankResult
		for _, doc := range in.Documents {
			out = append(out, domain.RerankResult{DocumentID: doc.ID, Score: .75})
		}
		return out, nil
	}))
	input := domain.SearchInput{UserID: "owner", Query: "Harbor", TopN: 3}
	found, err := e.SearchWithVariant(ctx, input, SearchVariantFull)
	if err != nil || found.Rerank.Status != "applied" || len(found.Candidates) != 3 {
		t.Fatalf("a source identity or complementary fact was dropped: %+v %v", found, err)
	}
	reader := &provenanceEvidenceReader{calls: map[string]int{}}
	e.SetSourceContextReader(reader)
	packet, err := e.PrepareEvidence(ctx, input, found.Candidates, EvidenceOptions{MaxBytes: 16000, NeighborTurns: 1})
	if err != nil || reader.calls["conversation-a"] != 1 || reader.calls["conversation-b"] != 1 || len(reader.calls) != 2 || packet.Diagnostics.PackedAnchorTurns != 2 {
		t.Fatalf("equal mention text collapsed independent source windows: %+v %+v %v", packet.Diagnostics, reader.calls, err)
	}
	byID := map[string]domain.MemoryDocument{}
	for _, doc := range packet.Memories {
		byID[doc.ID] = doc
	}
	for i, id := range []string{"a", "b"} {
		conversation := "conversation-" + id
		source := byID[fmt.Sprintf("source:%s:%d", conversation, i)]
		if _, rendered := byID[id]; rendered {
			t.Fatal("a bare mention reached the reader", id)
		}
		if source.Content != "Harbor" || source.Metadata["speaker"] != conversation+" speaker" || source.Metadata["source_time"] != time.Date(2026, 1, i+1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339) {
			t.Fatalf("source author, time or identity changed: %+v", source)
		}
	}
	if byID["c"].Content != "The bicycle inspection was postponed." {
		t.Fatal("mention handling displaced a complementary fact")
	}
}

func TestEvidenceSupplementCannotEvictProtectedOriginalPassages(t *testing.T) {
	original := coreFixture("fact", "source-a", "source-b")
	original[0].Content = "The inspection was postponed."
	for i := 1; i < len(original); i++ {
		original[i].Type = domain.MemoryTypeSegment
		original[i].ConversationID = original[i].ID
		original[i].Content = "We booked the inspection. The appointment was later cancelled."
		original[i].Metadata = map[string]any{"source_context": true, "source_anchor": true, "start_sequence": 1, "end_sequence": 1, "speaker": original[i].ID}
	}
	options := EvidenceOptions{MaxBytes: len(buildEvidenceSystemPrompt(original)), RequiredIDs: []string{"fact", "source-a", "source-b"}}
	supplement := coreFixture("supplement")
	supplement[0].Content = "Inspection complete."
	core := append(supplement, original...)
	packed := packEvidence(core, supplement, options)
	if len(packed) != len(original) {
		t.Fatalf("supplement displaced protected evidence: %+v", packed)
	}
	for i, doc := range packed {
		if doc.ID != original[i].ID || FormatEvidenceDocument(doc) != FormatEvidenceDocument(original[i]) {
			t.Fatal("a whole original passage or its source identity changed")
		}
	}
}
