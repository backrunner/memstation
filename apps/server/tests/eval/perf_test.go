package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	memoryservice "memstation/apps/server/internal/service/memory"
)

func TestPerformanceSmoke(t *testing.T) {
	t.Parallel()
	if os.Getenv("MEMSTATION_RUN_BENCHMARKS") != "true" {
		t.Skip("performance benchmark is opt-in; use scripts/eval/run-performance-smoke.sh")
	}

	dataset, err := LoadDataset(filepath.Join("testdata", "repo_native_golden.json"), DatasetFormatRepoNative)
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	report, err := RunPerformanceSmoke(context.Background(), dataset, PerformanceOptions{
		Variant:    memoryservice.SearchVariantFull,
		Iterations: 10,
		TopN:       5,
	})
	if err != nil {
		t.Fatalf("run performance smoke: %v", err)
	}
	if report.Embedding.Mode != EmbeddingModeDeterministic {
		t.Fatalf("expected performance smoke to default to deterministic embeddings, got %s", report.Embedding.Mode)
	}
	if report.P95Millis <= 0 {
		t.Fatalf("expected p95 to be positive, got %.3f", report.P95Millis)
	}
}
