package domain

import (
	"testing"
	"time"
)

func TestTimeDecayHigherImportanceExtendsFreshnessWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC)
	activityAt := now.Add(-45 * 24 * time.Hour)

	lowImportance := TimeDecay(MemoryTypeFact, activityAt, 1, 0.6, now)
	highImportance := TimeDecay(MemoryTypeFact, activityAt, 1, 1.8, now)

	if highImportance <= lowImportance {
		t.Fatalf("expected higher importance to decay more slowly, got low=%f high=%f", lowImportance, highImportance)
	}
}

func TestTimeDecayKeepsMonthOldSummariesRetrievable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC)
	activityAt := now.Add(-30 * 24 * time.Hour)

	summary := TimeDecay(MemoryTypeSummary, activityAt, 1, 1.2, now)
	segment := TimeDecay(MemoryTypeSegment, activityAt, 1, 1.2, now)

	if summary <= 0.6 {
		t.Fatalf("expected month-old summary to remain retrievable, got %f", summary)
	}
	if summary <= segment {
		t.Fatalf("expected summaries to decay more slowly than raw segments, got summary=%f segment=%f", summary, segment)
	}
}
