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
	if len(dataset.Documents) != 3 {
		t.Fatalf("expected dialogue-only corpus, got %d documents", len(dataset.Documents))
	}
	for _, doc := range dataset.Documents {
		if doc.Metadata["section"] != "conversation" {
			t.Fatal("gold/generated memory entered raw-dialogue index")
		}
		if doc.UserID != dataset.Queries[0].UserID {
			t.Fatal("speakers were split into different tenants")
		}
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

func TestLOCOMOOfficialSessionShapeExcludesGoldAndMetadataLeaves(t *testing.T) {
	sample := locomoSample{SampleID: "official", Conversation: map[string]any{
		"speaker_a": "Alex", "speaker_b": "Sam", "session_1_date_time": "3:30 pm on 8 May, 2023",
		"session_1": []any{map[string]any{"speaker": "Alex", "dia_id": "D1:1", "text": "I moved to Paris."}, map[string]any{"speaker": "Sam", "dia_id": "D1:2", "text": "I still live in London."}},
	}, EventSummary: "ANNOTATED GOLD"}
	docs := collectLOCOMODocuments(sample)
	if len(docs) != 2 {
		t.Fatalf("expected 2 turns, got %+v", docs)
	}
	for _, doc := range docs {
		if !strings.Contains(doc.Document.Content, "8 May, 2023") || !strings.Contains(doc.Document.Content, doc.Document.Metadata["speaker"].(string)+":") {
			t.Fatalf("missing speaker/date context: %+v", doc)
		}
	}
}
