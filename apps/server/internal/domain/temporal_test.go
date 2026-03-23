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
