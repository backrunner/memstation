package eval

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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

func TestPerformancePreservesHistoricalAndConversationQueries(t *testing.T) {
	t.Parallel()
	dataset, err := LoadDataset(filepath.Join("testdata", "repo_native_golden.json"), DatasetFormatRepoNative)
	if err != nil {
		t.Fatal(err)
	}
	queries := dataset.Queries[:0]
	for _, q := range dataset.Queries {
		if q.AsOf != nil || q.IncludeConversation {
			queries = append(queries, q)
		}
	}
	dataset.Queries = queries
	quality, err := RunBenchmark(context.Background(), dataset, RunOptions{Variants: []memoryservice.SearchVariant{memoryservice.SearchVariantFull}})
	if err != nil {
		t.Fatal(err)
	}
	perf, err := RunPerformanceSmoke(context.Background(), dataset, PerformanceOptions{Iterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(perf.QuerySamples) != len(quality.Variants[0].Cases) {
		t.Fatal("performance sample coverage differs from accuracy evaluation")
	}
	for i, c := range quality.Variants[0].Cases {
		want := make([]string, 0, len(c.Results))
		for _, result := range c.Results {
			want = append(want, result.Document.ID)
		}
		if !reflect.DeepEqual(perf.QuerySamples[i].ResultIDs, want) {
			t.Fatalf("%s measured a different retrieval path: got %v, want %v", c.CaseID, perf.QuerySamples[i].ResultIDs, want)
		}
		if c.CaseID == "graph.historical_validity" && (len(want) != 1 || want[0] != "history-hangzhou") {
			t.Fatalf("historical query did not retrieve its valid fact: %v", want)
		}
	}
}
