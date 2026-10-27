package eval

import (
	"context"
	"path/filepath"
	"testing"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

func TestRunVariantHonorsConversationAndSegmentFlags(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	for _, doc := range []domain.MemoryDocument{
		{
			ID:             "summary-freeze",
			NodeID:         "node-summary-freeze",
			UserID:         "user-1",
			ConversationID: "conv-freeze",
			Type:           domain.MemoryTypeSummary,
			Content:        "Friday evening deployment freeze details were captured in the planning note.",
			Importance:     1.1,
			DecayFactor:    1,
		},
		{
			ID:             "fact-freeze-owner",
			NodeID:         "node-fact-freeze-owner",
			UserID:         "user-1",
			ConversationID: "conv-freeze",
			Type:           domain.MemoryTypeFact,
			Content:        "Dana approves release exceptions.",
			Importance:     1,
			DecayFactor:    1,
		},
		{
			ID:             "segment-freeze",
			NodeID:         "node-segment-freeze",
			UserID:         "user-1",
			ConversationID: "conv-freeze",
			Type:           domain.MemoryTypeSegment,
			Content:        "user: remind me who approves release exceptions | assistant: acknowledged",
			Importance:     0.9,
			DecayFactor:    0.85,
		},
	} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store document %s: %v", doc.ID, err)
		}
	}

	dataset := Dataset{
		Name: "runner-flags",
		Queries: []QueryCase{
			{
				CaseID: "baseline",
				UserID: "user-1",
				Query:  "Friday evening deployment freeze details were captured in the planning note.",
				TopN:   3,
				Positives: []string{
					"summary-freeze",
				},
			},
			{
				CaseID:              "conversation",
				UserID:              "user-1",
				Query:               "Friday evening deployment freeze details were captured in the planning note.",
				TopN:                3,
				IncludeConversation: true,
				Positives: []string{
					"summary-freeze",
					"fact-freeze-owner",
				},
			},
			{
				CaseID:              "conversation-segments",
				UserID:              "user-1",
				Query:               "Friday evening deployment freeze details were captured in the planning note.",
				TopN:                3,
				IncludeConversation: true,
				IncludeSegments:     true,
				Positives: []string{
					"summary-freeze",
					"fact-freeze-owner",
					"segment-freeze",
				},
			},
		},
	}

	bench := &harness{
		engine: memoryservice.NewEngine(repo, nil),
		embedding: EmbeddingRunInfo{
			Mode:  EmbeddingModeDeterministic,
			Model: "benchmark-local-deterministic",
		},
	}
	report, _, err := runVariant(ctx, bench, dataset, memoryservice.SearchVariantFull, 0)
	if err != nil {
		t.Fatalf("run variant: %v", err)
	}

	caseReports := make(map[string]CaseReport, len(report.Cases))
	for _, item := range report.Cases {
		caseReports[item.CaseID] = item
	}

	if containsResult(caseReports["baseline"].Results, "fact-freeze-owner") {
		t.Fatalf("expected baseline query to exclude same-conversation fact without include_conversation, got %+v", caseReports["baseline"].Results)
	}
	if containsResult(caseReports["baseline"].Results, "segment-freeze") {
		t.Fatalf("expected baseline query to exclude segment without include_conversation, got %+v", caseReports["baseline"].Results)
	}

	if !containsResult(caseReports["conversation"].Results, "fact-freeze-owner") {
		t.Fatalf("expected include_conversation query to return fact companion, got %+v", caseReports["conversation"].Results)
	}
	if containsResult(caseReports["conversation"].Results, "segment-freeze") {
		t.Fatalf("expected include_conversation query to keep raw segment hidden when include_segments=false, got %+v", caseReports["conversation"].Results)
	}

	if !containsResult(caseReports["conversation-segments"].Results, "segment-freeze") {
		t.Fatalf("expected include_segments query to surface raw segment companion, got %+v", caseReports["conversation-segments"].Results)
	}
}

func containsResult(results []RankedResult, documentID string) bool {
	for _, item := range results {
		if item.Document.ID == documentID {
			return true
		}
	}
	return false
}

func TestRunBenchmarkPreservesMultimodalMetadataInResults(t *testing.T) {
	t.Parallel()

	dataset, err := LoadDataset(filepath.Join("testdata", "repo_native_golden.json"), DatasetFormatRepoNative)
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}

	report, err := RunBenchmark(context.Background(), dataset, RunOptions{
		Variants: []memoryservice.SearchVariant{memoryservice.SearchVariantFull},
		TopN:     3,
	})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}

	full := report.Variants[0]
	caseReport := findCaseReport(t, full.Cases, "retrieval.multimodal_tool_output_proxy")
	if len(caseReport.Results) == 0 {
		t.Fatalf("expected multimodal tool output case to return results")
	}
	top := caseReport.Results[0]
	if top.Document.ID != "summary-mm-tool-output-1" {
		t.Fatalf("expected top result to be multimodal tool output proxy, got %s", top.Document.ID)
	}

	metadata := top.Document.Metadata
	if !metadataHasString(metadata, "tool_payload_kind", "tool_output") {
		t.Fatalf("expected tool payload metadata to survive in result metadata, got %#v", metadata)
	}
	if !metadataHasNumber(metadata, "evidence_count", 2) {
		t.Fatalf("expected evidence_count=2, got %#v", metadata["evidence_count"])
	}
	if !metadataHasNumber(metadata, "source_part_count", 2) {
		t.Fatalf("expected source_part_count=2, got %#v", metadata["source_part_count"])
	}
	if !metadataHasBool(metadata, "has_source_blob", true) {
		t.Fatalf("expected has_source_blob=true, got %#v", metadata["has_source_blob"])
	}
	if !metadataHasSliceValue(metadata, "modalities", "video") || !metadataHasSliceValue(metadata, "modalities", "audio") {
		t.Fatalf("expected multimodal modalities to survive in metadata, got %#v", metadata["modalities"])
	}
}

func findCaseReport(t *testing.T, items []CaseReport, caseID string) CaseReport {
	t.Helper()
	for _, item := range items {
		if item.CaseID == caseID {
			return item
		}
	}
	t.Fatalf("case report %s not found", caseID)
	return CaseReport{}
}

func metadataHasString(metadata map[string]any, key, want string) bool {
	value, ok := metadata[key].(string)
	return ok && value == want
}

func metadataHasNumber(metadata map[string]any, key string, want float64) bool {
	switch value := metadata[key].(type) {
	case int:
		return float64(value) == want
	case int32:
		return float64(value) == want
	case int64:
		return float64(value) == want
	case float32:
		return float64(value) == want
	case float64:
		return value == want
	default:
		return false
	}
}

func metadataHasBool(metadata map[string]any, key string, want bool) bool {
	value, ok := metadata[key].(bool)
	return ok && value == want
}

func metadataHasSliceValue(metadata map[string]any, key, want string) bool {
	values, ok := metadata[key].([]any)
	if ok {
		for _, value := range values {
			if text, ok := value.(string); ok && text == want {
				return true
			}
		}
	}
	stringValues, ok := metadata[key].([]string)
	if ok {
		for _, value := range stringValues {
			if value == want {
				return true
			}
		}
	}
	return false
}
