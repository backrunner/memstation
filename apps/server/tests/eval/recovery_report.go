package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"memstation/apps/server/internal/worker"
)

func WriteRecoveryArtifacts(report worker.RecoveryReport, outDir string) error {
	if outDir == "" {
		return fmt.Errorf("output directory is required")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := writeRecoveryJSON(filepath.Join(outDir, "recovery.json"), report); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(recoveryMarkdownReport(report)), 0o644)
}

func writeRecoveryJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func recoveryMarkdownReport(report worker.RecoveryReport) string {
	var builder strings.Builder
	builder.WriteString("# MemStation Worker Recovery Drill\n\n")
	builder.WriteString(fmt.Sprintf("- Conversations: `%d`\n", report.Conversations))
	builder.WriteString(fmt.Sprintf("- Worker concurrency: `%d`\n", report.WorkerConcurrency))
	builder.WriteString(fmt.Sprintf("- Messages per turn: `%d`\n", report.MessagesPerTurn))
	builder.WriteString(fmt.Sprintf("- Peak backlog: `%d`\n", report.PeakBacklog))
	builder.WriteString(fmt.Sprintf("- Duration: `%.3fms`\n", report.DurationMillis))
	builder.WriteString(fmt.Sprintf("- Throughput: `%.3f tasks/s`\n", report.ThroughputTasksPerSecond))
	builder.WriteString(fmt.Sprintf("- Pending outbox: `%d`\n", report.PendingOutbox))
	builder.WriteString("\n## Completion\n\n")
	builder.WriteString("| Signal | Count |\n")
	builder.WriteString("| --- | ---: |\n")
	builder.WriteString(fmt.Sprintf("| Finalized conversations | %d |\n", report.CompletedConversations))
	builder.WriteString(fmt.Sprintf("| Completed summaries | %d |\n", report.SummaryCompleted))
	builder.WriteString(fmt.Sprintf("| Completed analyses | %d |\n", report.AnalysisCompleted))
	builder.WriteString(fmt.Sprintf("| Summary documents | %d |\n", report.SummaryDocuments))
	builder.WriteString(fmt.Sprintf("| Fact documents | %d |\n", report.FactDocuments))
	builder.WriteString(fmt.Sprintf("| Impression documents | %d |\n", report.ImpressionDocuments))
	builder.WriteString(fmt.Sprintf("| Generic memory documents | %d |\n", report.GenericDocuments))
	builder.WriteString(fmt.Sprintf("| Key memories | %d |\n", report.KeyMemories))
	builder.WriteString("\n## Task Timings\n\n")
	builder.WriteString("| Task | Count | P50 | P95 | Mean | Max |\n")
	builder.WriteString("| --- | ---: | ---: | ---: | ---: | ---: |\n")
	taskTypes := make([]string, 0, len(report.TaskTimings))
	for taskType := range report.TaskTimings {
		taskTypes = append(taskTypes, taskType)
	}
	sort.Strings(taskTypes)
	for _, taskType := range taskTypes {
		timing := report.TaskTimings[taskType]
		builder.WriteString(fmt.Sprintf("| `%s` | %d | %.3fms | %.3fms | %.3fms | %.3fms |\n",
			taskType,
			timing.Count,
			timing.P50Millis,
			timing.P95Millis,
			timing.MeanMillis,
			timing.MaxMillis,
		))
	}
	return builder.String()
}
