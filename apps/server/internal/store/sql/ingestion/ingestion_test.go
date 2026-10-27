package ingestion_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	ingestfixturestore "memstation/apps/server/internal/store/sql/internal/ingestfixture"

	"memstation/apps/server/internal/domain"
	ingestionservice "memstation/apps/server/internal/service/ingestion"
)

func TestIndependentIngestionAtomicIdempotency(t *testing.T) {
	db, repo, ctx, b := ingestfixturestore.IngestionFixture(t)
	receipt, err := repo.Observe(ctx, b, "batch1", 8192)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "accepted" || receipt.AcceptedSequence != 1 || receipt.IndexedSequence != 0 {
		t.Fatalf("premature index: %+v", receipt)
	}
	again, err := repo.Observe(ctx, b, "batch1", 8192)
	if err != nil || again.ID != receipt.ID || again.Events[0].Status != "duplicate" {
		t.Fatalf("duplicate %+v %v", again, err)
	}
	changed := b
	changed.Events = append([]domain.SourceEvent(nil), b.Events...)
	changed.Events[0].SourceMessageID = "other"
	if _, err = repo.Observe(ctx, changed, "batch1", 8192); !errors.Is(err, ingestionservice.ErrIdempotencyConflict) {
		t.Fatalf("batch collision: %v", err)
	}
	newEvent := b.Events[0]
	newEvent.EventID = "new"
	newEvent.SourceMessageID = "new-message"
	changed.Events = append([]domain.SourceEvent{newEvent}, changed.Events...)
	if _, err = repo.Observe(ctx, changed, "batch2", 8192); !errors.Is(err, ingestionservice.ErrIdempotencyConflict) {
		t.Fatalf("event collision: %v", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM source_messages WHERE workspace_id=$1`, b.WorkspaceID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial batch committed count=%d %v", count, err)
	}
	var window string
	var rev int64
	if err = db.QueryRow(`SELECT id,write_revision FROM conversations WHERE workspace_id=$1`, b.WorkspaceID).Scan(&window, &rev); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() { failures <- repo.FinalizeWindow(ctx, window, rev) })
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = db.QueryRow(`SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, window).Scan(&count); err != nil || count != 1 {
		t.Fatalf("finalization duplicate %d %v", count, err)
	}
	newEvent.EventID = "event2"
	b.Events = []domain.SourceEvent{newEvent}
	if _, err = repo.Observe(ctx, b, "next", 8192); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM conversations WHERE workspace_id=$1`, b.WorkspaceID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("late window %d %v", count, err)
	}
}

func TestIndependentIngestionDeleteBeforeCreate(t *testing.T) {
	db, repo, ctx, b := ingestfixturestore.IngestionFixture(t)
	create := b.Events[0]
	remove := create
	remove.EventID = "delete"
	remove.Type = "message.deleted"
	remove.SourceRevision = 2
	remove.Parts = nil
	b.Events = []domain.SourceEvent{remove}
	first, err := repo.Observe(ctx, b, "delete", 8192)
	if err != nil {
		t.Fatal(err)
	}
	b.Events = []domain.SourceEvent{create}
	if _, err = repo.Observe(ctx, b, "late-create", 8192); err != nil {
		t.Fatal(err)
	}
	create.SourceRevision = 3
	create.EventID = "late-newer"
	b.Events = []domain.SourceEvent{create}
	if _, err = repo.Observe(ctx, b, "late-newer", 8192); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Source(ctx, first.Events[0].MessageID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatalf("source resurrected %v", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM messages WHERE workspace_id=$1`, b.WorkspaceID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted projection recreated %d %v", count, err)
	}
}

func TestIndependentIngestionDeletionDoesNotRequireNewMessageRevision(t *testing.T) {
	for _, revision := range []int64{1, 2} {
		t.Run(fmt.Sprint(revision), func(t *testing.T) {
			_, repo, ctx, batch := ingestfixturestore.IngestionFixture(t)
			batch.Events[0].SourceRevision = 2
			accepted, err := repo.Observe(ctx, batch, "created", 8192)
			if err != nil {
				t.Fatal(err)
			}
			deleted := batch.Events[0]
			deleted.EventID = "delete"
			deleted.Type = "message.deleted"
			deleted.SourceRevision = revision
			deleted.Parts = nil
			batch.Events = []domain.SourceEvent{deleted}
			if _, err = repo.Observe(ctx, batch, "deleted", 8192); err != nil {
				t.Fatalf("trusted deletion must override an equal or older revision: %v", err)
			}
			if _, err = repo.Source(ctx, accepted.Events[0].MessageID); !errors.Is(err, domain.ErrAccessNotFound) {
				t.Fatalf("deleted source remains readable: %v", err)
			}
		})
	}
}

func TestIndependentIngestionActorAndUnicodeReferences(t *testing.T) {
	_, repo, ctx, b := ingestfixturestore.IngestionFixture(t)
	receipt, err := repo.Observe(ctx, b, "one", 8192)
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.Source(ctx, receipt.Events[0].MessageID)
	if err != nil || source.SpeakerActorID == "" {
		t.Fatalf("actor missing %+v %v", source, err)
	}
	ref := domain.SourceRef{WorkspaceID: b.WorkspaceID, MessageID: source.ID, Revision: 1, PartIndex: 0, Span: &domain.TextSpan{Start: 0, End: 7}}
	event := b.Events[0]
	event.EventID = "quote"
	event.SourceMessageID = "quote"
	event.Speaker.ExternalID = "bob"
	event.Parts = []domain.SourcePart{{Type: "quote", Text: "我在上海工作。", Source: &ref, Author: &domain.SourceSpeaker{Kind: "human", ExternalID: "alice"}}}
	b.Events = []domain.SourceEvent{event}
	quoted, err := repo.Observe(ctx, b, "quote", 8192)
	if err != nil {
		t.Fatal(err)
	}
	value, err := repo.Source(ctx, quoted.Events[0].MessageID)
	if err != nil || value.SpeakerActorID == source.SpeakerActorID || value.Parts[0].Author.ActorID != source.SpeakerActorID {
		t.Fatalf("quotation attribution %+v %v", value, err)
	}
	ref.Span.End = 8
	event.EventID = "bad"
	event.SourceMessageID = "bad"
	b.Events = []domain.SourceEvent{event}
	if _, err = repo.Observe(ctx, b, "bad-span", 8192); !errors.Is(err, ingestionservice.ErrInvalidEvent) {
		t.Fatalf("invalid codepoint span %v", err)
	}
}

func TestIndependentIngestionQuoteRejectsContradictoryAuthor(t *testing.T) {
	_, repo, ctx, b := ingestfixturestore.IngestionFixture(t)
	receipt, err := repo.Observe(ctx, b, "original", 8192)
	if err != nil {
		t.Fatal(err)
	}
	e := b.Events[0]
	e.EventID = "quote-spoof"
	e.SourceMessageID = "quote-spoof"
	e.Speaker.ExternalID = "bob"
	e.Parts = []domain.SourcePart{{Type: "quote", Text: "我在上海工作。", Author: &domain.SourceSpeaker{Kind: "human", ExternalID: "charlie"}, Source: &domain.SourceRef{WorkspaceID: b.WorkspaceID, MessageID: receipt.Events[0].MessageID, Revision: 1, PartIndex: 0}}}
	b.Events = []domain.SourceEvent{e}
	if _, err = repo.Observe(ctx, b, "spoof", 8192); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("misattributed quotation accepted: %v", err)
	}
	e.Parts[0].Author = nil
	e.Parts[0].Text = "完全不同的原话"
	b.Events = []domain.SourceEvent{e}
	if _, err = repo.Observe(ctx, b, "changed-quote", 8192); !errors.Is(err, ingestionservice.ErrInvalidEvent) {
		t.Fatalf("fabricated quoted text accepted: %v", err)
	}
}
