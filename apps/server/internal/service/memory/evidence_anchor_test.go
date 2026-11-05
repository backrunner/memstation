package memory

import (
	"context"
	"slices"
	"testing"

	"memstation/apps/server/internal/domain"
	memorystore "memstation/apps/server/internal/store/sql/memory"
)

type anchorEvidenceReader struct{ calls int }

func (r *anchorEvidenceReader) ReadSourceWindow(_ context.Context, in domain.SourceWindowInput) ([]domain.SourceTurn, error) {
	r.calls++
	var turns []domain.SourceTurn
	for _, seq := range []int{0, 1, 2, 3} {
		near := false
		for _, anchor := range in.Sequences {
			near = near || (seq >= anchor-in.Radius && seq <= anchor+in.Radius)
		}
		if !near {
			continue
		}
		turns = append(turns, domain.SourceTurn{UserID: in.UserID, WorkspaceID: in.WorkspaceID, ConversationID: in.ConversationID, Sequence: seq, Text: "A complete original statement.", Anchor: slices.Contains(in.Sequences, seq)})
	}
	return turns, nil
}

func TestEvidenceBudgetPrioritizesDirectSupportAcrossCandidateWindows(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	var docs []domain.MemoryDocument
	for i, id := range []string{"first", "second"} {
		doc := domain.MemoryDocument{ID: id, UserID: "owner", ConversationID: "conversation", Type: domain.MemoryTypeFact, Content: "A retained derived statement.", Metadata: map[string]any{"source_conversation_revision": int64(1), "source_quotes": []map[string]any{{"sequence_num": i + 2, "sequence_base": 1}}}}
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
		stored, err := repo.GetDocument(ctx, "owner", id, domain.MemoryTypeFact)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, stored)
	}
	reader := &anchorEvidenceReader{}
	engine := NewEngine(repo, nil)
	engine.SetSourceContextReader(reader)
	packet, err := engine.PrepareEvidence(ctx, domain.SearchInput{UserID: "owner"}, docs, EvidenceOptions{MaxBytes: 380, NeighborTurns: 1})
	if err != nil || reader.calls != 2 || packet.Diagnostics.PackedSourceTurns != 2 || packet.Diagnostics.PackedAnchorTurns != 2 || len(packet.Text) > 380 {
		t.Fatalf("direct support admission: diagnostics=%+v calls=%d bytes=%d error=%v", packet.Diagnostics, reader.calls, len(packet.Text), err)
	}
	for _, doc := range packet.Memories {
		if doc.Metadata["source_context"] == true && doc.Metadata["source_anchor"] != true {
			t.Fatal("neighbor crowded out a direct citation")
		}
	}
}
