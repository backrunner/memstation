package eval

import (
	"math"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestCountMatchesDoesNotRewardDuplicateDocuments(t *testing.T) {
	results := []RankedResult{
		{Document: domain.MemoryDocument{ID: "positive"}},
		{Document: domain.MemoryDocument{ID: "positive"}},
		{Document: domain.MemoryDocument{ID: "other"}},
	}
	if got := recallAt(results, map[string]struct{}{"positive": {}}, 3); got != 1 {
		t.Fatalf("expected duplicate result to count once, got %f", got)
	}
}

func TestNDCGPenalizesTruncationAndDuplicateRanks(t *testing.T) {
	positives := map[string]struct{}{"a": {}, "b": {}}
	graded := map[string]float64{"a": 2, "b": 1}
	ideal := 2 + 1/math.Log2(3)
	for _, tc := range []struct {
		name string
		ids  []string
		want float64
	}{
		{"complete", []string{"a", "b"}, 1},
		{"truncated", []string{"a"}, 2 / ideal},
		{"duplicate", []string{"a", "a", "b"}, 2.5 / ideal},
		{"empty", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var results []RankedResult
			for _, id := range tc.ids {
				results = append(results, RankedResult{Document: domain.MemoryDocument{ID: id}})
			}
			if got := ndcgAt(results, graded, positives, 5); math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("nDCG = %f, want %f", got, tc.want)
			}
		})
	}
}

func TestRelationHitRateDoesNotRewardDuplicates(t *testing.T) {
	results := []RankedResult{{Document: domain.MemoryDocument{ID: "a"}}, {Document: domain.MemoryDocument{ID: "a"}}}
	if got := relationHitRate(results, map[string]struct{}{"a": {}, "b": {}}); got != 0.5 {
		t.Fatalf("relation hit rate = %f, want 0.5", got)
	}
}

func TestWrongUserLeakRejectsUnownedDocuments(t *testing.T) {
	results := []RankedResult{{Document: domain.MemoryDocument{ID: "orphan"}}}
	if !wrongUserLeak(results, "user-a") {
		t.Fatal("expected an unowned result to be reported as an isolation leak")
	}
}
