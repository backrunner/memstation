package analysis

import (
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestIdenticalFactTextKeepsDistinctTrustedActors(t *testing.T) {
	makeDoc := func(actors ...string) domain.MemoryDocument {
		return applyFactLineageMetadata(domain.MemoryDocument{Type: domain.MemoryTypeFact, Content: "Alex prefers tea", SubjectActorIDs: actors, Metadata: map[string]any{"fact_key": "drink", "subject": "Alex"}}, "owner", "window")
	}
	a, b := makeDoc("actor-a"), makeDoc("actor-b")
	if a.Metadata["lineage_revision_key"] == b.Metadata["lineage_revision_key"] {
		t.Fatal("same-name actors collapsed into one fact revision")
	}
	if makeDoc("actor-a", "actor-b").Metadata["lineage_revision_key"] != makeDoc("actor-b", "actor-a").Metadata["lineage_revision_key"] {
		t.Fatal("subject ordering changed stable revision")
	}
}
