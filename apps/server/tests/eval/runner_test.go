package eval

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
	sqlstore "memstation/apps/server/internal/store/sql"
)

func TestRunVariantHonorsConversationAndSegmentFlags(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
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
			Content:        "Dana owns the freeze exception approval.",
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
