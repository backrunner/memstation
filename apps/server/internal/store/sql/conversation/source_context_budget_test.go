package conversation

import (
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestSourceBudgetRetainsWholeCitationsAndRestoresChronology(t *testing.T) {
	turns := []domain.SourceTurn{
		{Sequence: 0, Text: "neighbor"},
		{Sequence: 1, Text: "first", Anchor: true},
		{Sequence: 2, Text: "neighbor"},
		{Sequence: 3, Text: "second", Anchor: true},
	}
	got := boundSourceTurns(turns, domain.SourceWindowInput{Limit: 3, MaxBytes: 11})
	if len(got) != 2 || got[0].Sequence != 1 || got[1].Sequence != 3 || got[0].Text != "first" || got[1].Text != "second" {
		t.Fatalf("direct support was displaced or truncated: %+v", got)
	}
	got = boundSourceTurns(turns, domain.SourceWindowInput{Limit: 3, MaxBytes: 30})
	if len(got) != 3 || got[0].Sequence != 0 || got[1].Sequence != 1 || got[2].Sequence != 3 {
		t.Fatalf("selected window is not chronological: %+v", got)
	}
}
