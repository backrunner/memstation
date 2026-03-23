package eval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	memoryservice "memstation/apps/server/internal/service/memory"
)

func TestLOCOMOAdapterSmoke(t *testing.T) {
	t.Parallel()

	dataset, err := LoadLOCOMODataset(filepath.Join("testdata", "locomo_fixture.json"), LOCOMOOptions{SkipCategoryFive: true})
	if err != nil {
		t.Fatalf("load locomo fixture: %v", err)
	}
	if len(dataset.Queries) != 2 {
		t.Fatalf("expected 2 LOCOMO queries after skipping category 5, got %d", len(dataset.Queries))
	}
	for _, query := range dataset.Queries {
		if strings.HasSuffix(query.CaseID, ".03") {
			t.Fatalf("category 5 fixture query should be skipped, found %s", query.CaseID)
		}
	}
	report, err := RunBenchmark(context.Background(), dataset, RunOptions{
		Variants: []memoryservice.SearchVariant{memoryservice.SearchVariantFull},
		TopN:     5,
	})
	if err != nil {
		t.Fatalf("run locomo smoke benchmark: %v", err)
	}
	if len(report.Variants) != 1 || len(report.Variants[0].Cases) != 2 {
		t.Fatalf("unexpected LOCOMO report shape: %+v", report)
	}
}
