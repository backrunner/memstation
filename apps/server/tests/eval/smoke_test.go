package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	memoryservice "memstation/apps/server/internal/service/memory"
)

func TestRepoNativeSmokeBenchmark(t *testing.T) {
	t.Parallel()

	dataset, err := LoadDataset(filepath.Join("testdata", "repo_native_golden.json"), DatasetFormatRepoNative)
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	report, err := RunBenchmark(context.Background(), dataset, RunOptions{
		Variants: []memoryservice.SearchVariant{
			memoryservice.SearchVariantFull,
			memoryservice.SearchVariantFullNoRelations,
			memoryservice.SearchVariantSemanticOnly,
			memoryservice.SearchVariantSemanticImportance,
		},
		TopN: 5,
	})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if report.Embedding.Mode != EmbeddingModeDeterministic {
		t.Fatalf("expected smoke benchmark to default to deterministic embeddings, got %s", report.Embedding.Mode)
	}
	if err := ValidateSmokeExpectations(report); err != nil {
		t.Fatalf("validate smoke expectations: %v", err)
	}
	outDir := t.TempDir()
	if err := WriteArtifacts(report, outDir); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}
	for _, name := range []string{"aggregate.json", "results.json", "failures.json", "report.md"} {
		if _, err := osStat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("expected artifact %s: %v", name, err)
		}
	}
}

func osStat(path string) (bool, error) {
	_, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return true, nil
}
