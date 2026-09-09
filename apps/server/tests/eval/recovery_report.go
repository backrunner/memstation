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
	fmt.Fprintf(&builder, "- Conversations: `%d`\n", report.Conversations)
	fmt.Fprintf(&builder, "- Worker concurrency: `%d`\n", report.WorkerConcurrency)
	fmt.Fprintf(&builder, "- Messages per turn: `%d`\n", report.MessagesPerTurn)
	fmt.Fprintf(&builder, "- Peak backlog: `%d`\n", report.PeakBacklog)
	fmt.Fprintf(&builder, "- Duration: `%.3fms`\n", report.DurationMillis)
	fmt.Fprintf(&builder, "- Throughput: `%.3f tasks/s`\n", report.ThroughputTasksPerSecond)
	fmt.Fprintf(&builder, "- Pending outbox: `%d`\n", report.PendingOutbox)
	builder.WriteString("\n## Completion\n\n")
	builder.WriteString("| Signal | Count |\n")
	builder.WriteString("| --- | ---: |\n")
	fmt.Fprintf(&builder, "| Finalized conversations | %d |\n", report.CompletedConversations)
	fmt.Fprintf(&builder, "| Completed summaries | %d |\n", report.SummaryCompleted)
	fmt.Fprintf(&builder, "| Completed analyses | %d |\n", report.AnalysisCompleted)
	fmt.Fprintf(&builder, "| Summary documents | %d |\n", report.SummaryDocuments)
	fmt.Fprintf(&builder, "| Fact documents | %d |\n", report.FactDocuments)
	fmt.Fprintf(&builder, "| Impression documents | %d |\n", report.ImpressionDocuments)
	fmt.Fprintf(&builder, "| Generic memory documents | %d |\n", report.GenericDocuments)
	fmt.Fprintf(&builder, "| Key memories | %d |\n", report.KeyMemories)
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
		fmt.Fprintf(&builder, "| `%s` | %d | %.3fms | %.3fms | %.3fms | %.3fms |\n",
			taskType,
			timing.Count,
			timing.P50Millis,
			timing.P95Millis,
			timing.MeanMillis,
			timing.MaxMillis)
	}
	return builder.String()
}
