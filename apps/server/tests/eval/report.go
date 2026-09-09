package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func WriteArtifacts(report BenchmarkReport, outDir string) error {
	if outDir == "" {
		return fmt.Errorf("output directory is required")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	aggregate := struct {
		Dataset      string              `json:"dataset"`
		GeneratedAt  string              `json:"generated_at"`
		Embedding    EmbeddingRunInfo    `json:"embedding"`
		Variants     []VariantSummary    `json:"variants"`
		Comparisons  []VariantComparison `json:"comparisons,omitempty"`
		Expectations SmokeExpectations   `json:"expectations,omitempty"`
	}{
		Dataset:      report.Dataset,
		GeneratedAt:  report.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"),
		Embedding:    report.Embedding,
		Comparisons:  report.Comparisons,
		Expectations: report.Expectations,
	}
	for _, item := range report.Variants {
		aggregate.Variants = append(aggregate.Variants, VariantSummary{
			Variant:   string(item.Variant),
			Aggregate: item.Aggregate,
		})
	}
	if err := writeJSON(filepath.Join(outDir, "aggregate.json"), aggregate); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "results.json"), report.Variants); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "failures.json"), report.Failures); err != nil {
		return err
	}
	if report.Performance != nil {
		if err := writeJSON(filepath.Join(outDir, "perf.json"), report.Performance); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(markdownReport(report)), 0o644)
}

type VariantSummary struct {
	Variant   string           `json:"variant"`
	Aggregate AggregateMetrics `json:"aggregate"`
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func markdownReport(report BenchmarkReport) string {
	var builder strings.Builder
	builder.WriteString("# MemStation Memory Benchmark\n\n")
	fmt.Fprintf(&builder, "- Dataset: `%s`\n", report.Dataset)
	fmt.Fprintf(&builder, "- Generated at: `%s`\n", report.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&builder, "- Embedding mode: `%s`\n", report.Embedding.Mode)
	if report.Embedding.Provider != "" {
		fmt.Fprintf(&builder, "- Embedding provider: `%s`\n", report.Embedding.Provider)
	}
	if report.Embedding.Model != "" {
		fmt.Fprintf(&builder, "- Embedding model: `%s`\n", report.Embedding.Model)
	}
	if report.Embedding.BaseURL != "" {
		fmt.Fprintf(&builder, "- Embedding endpoint: `%s`\n", report.Embedding.BaseURL)
	}
	if report.Description != "" {
		fmt.Fprintf(&builder, "- Description: %s\n", report.Description)
	}
	builder.WriteString("\n## Aggregate Metrics\n\n")
	builder.WriteString("| Variant | Recall@3 | MRR | nDCG@5 | Negative leakage | Wrong-user leakage | Stale top3 | Relation hit |\n")
	builder.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, item := range report.Variants {
		fmt.Fprintf(&builder, "| `%s` | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f |\n",
			item.Variant,
			item.Aggregate.RecallAt3,
			item.Aggregate.MRR,
			item.Aggregate.NDCGAt5,
			item.Aggregate.NegativeLeakage,
			item.Aggregate.WrongUserLeakage,
			item.Aggregate.StaleFactTop3Rate,
			item.Aggregate.RelationPositiveHitRate)
	}
	if len(report.Comparisons) > 0 {
		builder.WriteString("\n## Variant Comparisons\n\n")
		builder.WriteString("| Baseline | Candidate | Recall@3 delta | MRR delta | nDCG@5 delta | Relation gain | Negative leak delta |\n")
		builder.WriteString("| --- | --- | ---: | ---: | ---: | ---: | ---: |\n")
		for _, item := range report.Comparisons {
			fmt.Fprintf(&builder, "| `%s` | `%s` | %.3f | %.3f | %.3f | %.3f | %.3f |\n",
				item.BaselineVariant,
				item.CandidateVariant,
				item.RecallAt3Delta,
				item.MRRDelta,
				item.NDCGAt5Delta,
				item.RelationPositiveGain,
				item.NegativeLeakageDelta)
		}
	}
	if report.Performance != nil {
		builder.WriteString("\n## Performance Smoke\n\n")
		fmt.Fprintf(&builder, "- Variant: `%s`\n", report.Performance.Variant)
		fmt.Fprintf(&builder, "- Embedding mode: `%s`\n", report.Performance.Embedding.Mode)
		if report.Performance.Embedding.Model != "" {
			fmt.Fprintf(&builder, "- Embedding model: `%s`\n", report.Performance.Embedding.Model)
		}
		fmt.Fprintf(&builder, "- Samples: `%d`\n", report.Performance.Samples)
		fmt.Fprintf(&builder, "- P50: `%.3fms`\n", report.Performance.P50Millis)
		fmt.Fprintf(&builder, "- P95: `%.3fms`\n", report.Performance.P95Millis)
		fmt.Fprintf(&builder, "- Mean: `%.3fms`\n", report.Performance.MeanMillis)
	}
	if len(report.Failures) > 0 {
		builder.WriteString("\n## Failure Samples\n\n")
		for _, item := range report.Failures {
			fmt.Fprintf(&builder, "- `%s` / `%s`: %s\n", item.Variant, item.CaseID, strings.Join(item.Reasons, ", "))
		}
	}
	return builder.String()
}
