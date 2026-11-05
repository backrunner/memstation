package memory

import (
	"context"
	"reflect"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestAdmissionDiagnosticsExplainUnscoredExpansionWithoutChangingInference(t *testing.T) {
	e := NewEngine(nil, nil)
	e.SetRerankConfig(RerankConfig{Model: "test:model", MinCandidates: 2, MaxCandidates: 2})
	pool := []searchCandidate{
		{Document: domain.MemoryDocument{NodeID: "mention-a", Type: domain.MemoryTypeEntity, Content: "Harbor", Metadata: map[string]any{"interpretation": "source_mention"}}, BaseSemanticScore: .9, PreliminaryScore: .9},
		{Document: domain.MemoryDocument{NodeID: "mention-b", Type: domain.MemoryTypeEntity, Content: "Harbor", Metadata: map[string]any{"interpretation": "source_mention"}}, KeywordScore: .8, PreliminaryScore: .8},
		{Document: domain.MemoryDocument{NodeID: "companion", Type: domain.MemoryTypeFact, Content: "The visit was cancelled."}, ConversationBoost: .4},
	}
	original := append([]searchCandidate(nil), pool...)
	e.SetReranker(rerankFunc(func(_ context.Context, in domain.RerankInput) ([]domain.RerankResult, error) {
		want := []domain.RerankDocument{{ID: "mention-a", Text: "[entity] Harbor"}, {ID: "mention-b", Text: "[entity] Harbor"}}
		if !reflect.DeepEqual(in.Documents, want) || in.Query != "Harbor visit" {
			t.Fatalf("diagnostic changed model input: %+v", in)
		}
		return []domain.RerankResult{{DocumentID: "mention-a", Score: .8}, {DocumentID: "mention-b", Score: .7}}, nil
	}))
	scores, ranks, diagnostic := e.rerankCandidates(context.Background(), domain.SearchInput{Query: "Harbor visit", TopN: 2}, pool)
	want := RerankAdmissionDiagnostics{
		QueryScored:   RerankAdmissionCounts{Available: 2, Submitted: 2},
		ExpansionOnly: RerankAdmissionCounts{Available: 1},
		SourceMention: RerankAdmissionCounts{Available: 2, Submitted: 2},
	}
	if diagnostic.Admission != want || diagnostic.Status != "applied" || scores["mention-a"] != .8 || ranks["mention-b"] != 2 || !reflect.DeepEqual(pool, original) {
		t.Fatalf("boundary counts or ranking changed: %+v", diagnostic)
	}
}

func TestAdmissionDiagnosticsCountValidIdentitiesOnce(t *testing.T) {
	pool := []searchCandidate{
		{Document: domain.MemoryDocument{NodeID: "a", Content: "fact"}, SupersessionBoost: .4},
		{Document: domain.MemoryDocument{NodeID: "a", Content: "fact"}, SupersessionBoost: .4},
		{Document: domain.MemoryDocument{NodeID: "empty"}, ConversationBoost: .4},
		{Document: domain.MemoryDocument{Content: "missing identity"}, ConversationBoost: .4},
	}
	got := describeRerankAdmission(pool, map[string]bool{"a": true})
	if got.ExpansionOnly != (RerankAdmissionCounts{Available: 1, Submitted: 1}) || got.QueryScored.Available != 0 {
		t.Fatalf("invalid or duplicate documents inflated counts: %+v", got)
	}
}
