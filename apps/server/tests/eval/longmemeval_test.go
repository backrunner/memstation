package eval

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	memoryservice "memstation/apps/server/internal/service/memory"
)

func TestLongMemEvalSessionAdapterSmoke(t *testing.T) {
	t.Parallel()

	dataset, err := LoadLongMemEvalDataset(filepath.Join("testdata", "longmemeval_fixture.json"), LongMemEvalOptions{
		Granularity: LongMemEvalGranularitySession,
	})
	if err != nil {
		t.Fatalf("load LongMemEval fixture: %v", err)
	}
	if len(dataset.Queries) != 2 {
		t.Fatalf("expected 2 LongMemEval queries after skipping abstention, got %d", len(dataset.Queries))
	}
	for _, query := range dataset.Queries {
		if strings.Contains(query.CaseID, "_abs") {
			t.Fatalf("abstention query should be skipped, found %s", query.CaseID)
		}
		if query.TopN != 10 {
			t.Fatalf("expected session granularity topN 10, got %d", query.TopN)
		}
		if len(query.MemoryTypes) != 1 || query.MemoryTypes[0] != "segment" {
			t.Fatalf("expected session queries to search only segments, got %+v", query.MemoryTypes)
		}
	}
	for _, doc := range dataset.Documents {
		if doc.Type != "segment" {
			t.Fatalf("expected session documents to be stored as segments, got %s", doc.Type)
		}
	}

	report, err := RunBenchmark(context.Background(), dataset, RunOptions{
		Variants: []memoryservice.SearchVariant{memoryservice.SearchVariantFull},
		TopN:     5,
	})
	if err != nil {
		t.Fatalf("run LongMemEval smoke benchmark: %v", err)
	}
	if len(report.Variants) != 1 || len(report.Variants[0].Cases) != 2 {
		t.Fatalf("unexpected LongMemEval report shape: %+v", report)
	}
}

func TestLongMemEvalTurnAdapterUsesTurnEvidence(t *testing.T) {
	t.Parallel()

	dataset, err := LoadLongMemEvalDataset(filepath.Join("testdata", "longmemeval_fixture.json"), LongMemEvalOptions{
		Granularity: LongMemEvalGranularityTurn,
	})
	if err != nil {
		t.Fatalf("load LongMemEval fixture: %v", err)
	}
	if len(dataset.Queries) != 2 {
		t.Fatalf("expected 2 LongMemEval queries after skipping abstention, got %d", len(dataset.Queries))
	}
	if dataset.Queries[0].TopN != 20 {
		t.Fatalf("expected turn granularity topN 20, got %d", dataset.Queries[0].TopN)
	}
	if len(dataset.Queries[0].MemoryTypes) != 1 || dataset.Queries[0].MemoryTypes[0] != "segment" {
		t.Fatalf("expected turn queries to search only segments, got %+v", dataset.Queries[0].MemoryTypes)
	}
	for _, positive := range dataset.Queries[0].Positives {
		if !strings.Contains(positive, "-turn-") {
			t.Fatalf("expected turn-level positive id, got %s", positive)
		}
	}
}

func TestLongMemEvalAdapterAllowsRepeatedSessionIDs(t *testing.T) {
	t.Parallel()

	samples := []longMemEvalSample{{
		QuestionID:         "q-duplicate-session",
		QuestionType:       "single-session-user",
		Question:           "Who approves the freeze exception?",
		QuestionDate:       "2025-03-10",
		HaystackSessionIDs: []any{"sharegpt-syblhtk-0", "sharegpt-syblhtk-0"},
		HaystackDates:      []any{"2025-03-01", "2025-03-02"},
		HaystackSessions: [][]longMemEvalTurn{
			{
				{Role: "user", Content: "Mina approves freeze exceptions."},
			},
			{
				{Role: "assistant", Content: "Noted that Mina approves the exception.", HasAnswer: true},
			},
		},
		AnswerSessionIDs: []any{"sharegpt-syblhtk-0"},
	}}

	for _, granularity := range []LongMemEvalGranularity{LongMemEvalGranularitySession, LongMemEvalGranularityTurn} {
		dataset, err := buildLongMemEvalDataset(samples, LongMemEvalOptions{Granularity: granularity})
		if err != nil {
			t.Fatalf("build dataset with %s granularity: %v", granularity, err)
		}
		if err := dataset.Validate(); err != nil {
			t.Fatalf("validate dataset with %s granularity: %v", granularity, err)
		}
		seen := map[string]struct{}{}
		for _, doc := range dataset.Documents {
			if _, ok := seen[doc.ID]; ok {
				t.Fatalf("duplicate document id for %s granularity: %s", granularity, doc.ID)
			}
			seen[doc.ID] = struct{}{}
		}
	}
}
