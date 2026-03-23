package eval

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"memstation/apps/server/internal/worker"
)

func TestWriteRecoveryArtifacts(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	report := worker.RecoveryReport{
		GeneratedAt:              time.Now().UTC(),
		Conversations:            8,
		WorkerConcurrency:        4,
		MessagesPerTurn:          3,
		TotalTasks:               40,
		PeakBacklog:              12,
		DurationMillis:           24.5,
		ThroughputTasksPerSecond: 1632.6,
		PendingOutbox:            0,
		TaskTimings: map[string]worker.TaskTimingSummary{
			"conversation.finalize": {
				Count:      8,
				P50Millis:  1.2,
				P95Millis:  2.4,
				MeanMillis: 1.5,
				MaxMillis:  2.8,
			},
		},
	}
	if err := WriteRecoveryArtifacts(report, outDir); err != nil {
		t.Fatalf("write recovery artifacts: %v", err)
	}
	for _, name := range []string{"recovery.json", "report.md"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("expected recovery artifact %s: %v", name, err)
		}
	}
}
