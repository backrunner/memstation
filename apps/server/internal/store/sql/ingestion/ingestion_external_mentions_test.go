package ingestion_test

import (
	"errors"
	"testing"

	ingestfixturestore "memstation/apps/server/internal/store/sql/internal/ingestfixture"

	"memstation/apps/server/internal/domain"
)

func TestIndependentIngestionFirstMentionExternalIdentity(t *testing.T) {
	_, repo, ctx, batch := ingestfixturestore.IngestionFixture(t)
	batch.Events[0].Parts = append(batch.Events[0].Parts, domain.SourcePart{
		Type: "mention", Author: &domain.SourceSpeaker{Kind: "human", ExternalID: "new-member", DisplayName: "小周"},
	})
	accepted, err := repo.Observe(ctx, batch, "external-mention", 8192)
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.Source(ctx, accepted.Events[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	mention := source.Parts[1]
	if mention.ActorID == "" || mention.Author == nil || mention.ActorID != mention.Author.ActorID || mention.ActorID == source.SpeakerActorID {
		t.Fatalf("external mention identity missing: %+v", mention)
	}
	// Caller-supplied IDs must agree with the Integration-bound identity.
	event := batch.Events[0]
	event.EventID = "contradictory-mention"
	event.SourceMessageID = "contradictory-mention"
	event.Parts = []domain.SourcePart{{Type: "mention", ActorID: source.SpeakerActorID, Author: &domain.SourceSpeaker{Kind: "human", ExternalID: "new-member"}}}
	batch.Events = []domain.SourceEvent{event}
	if _, err = repo.Observe(ctx, batch, "contradictory-mention", 8192); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("contradictory mention accepted: %v", err)
	}
}
