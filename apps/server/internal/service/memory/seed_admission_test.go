package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	memorystore "memstation/apps/server/internal/store/sql/memory"
)

func TestChannelAdmissionPreservesLowScaleEvidenceAndExpansionRoom(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	vectorResults := []domain.VectorSearchResult{}
	for i := range 128 {
		id := fmt.Sprintf("direct-%03d", i)
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{UserID: "owner", ID: id, NodeID: id, Type: domain.MemoryTypeEvent, Content: "Direct evidence."}); err != nil {
			t.Fatal(err)
		}
		vectorResults = append(vectorResults, domain.VectorSearchResult{NodeID: id, Score: .95 - float64(i)/1000})
	}
	for _, doc := range []domain.MemoryDocument{
		{UserID: "owner", ID: "mention", NodeID: "mention", Type: domain.MemoryTypeEntity, Content: "Harbor", Metadata: map[string]any{"canonical_name": "Harbor", "interpretation": "source_mention"}},
		{UserID: "owner", ID: "posting", NodeID: "posting", Type: domain.MemoryTypeEvent, Content: "Source with the literal mention."},
		{UserID: "owner", ID: "source", NodeID: "source", Type: domain.MemoryTypeEvent, Content: "A distinct source event."},
	} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{UserID: "owner", SrcNodeID: "posting", DstNodeID: "mention", RelationType: domain.MemoryRelationMentionsEntity, Weight: 1}); err != nil {
		t.Fatal(err)
	}
	if err := repo.LinkDocuments(ctx, domain.MemoryRelation{UserID: "owner", SrcNodeID: "posting", DstNodeID: "source", RelationType: domain.MemoryRelationSupports, Weight: 1}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(entityAdmissionRepo{repo}, directRecallVector{search: func(context.Context) ([]domain.VectorSearchResult, error) { return vectorResults, nil }})
	e.SetEntityRecall(queryExtractorFunc(func(context.Context, string, string) (domain.QueryEntities, error) {
		return domain.QueryEntities{Names: []string{"Harbor"}}, nil
	}), time.Second)
	found, err := e.SearchWithVariant(ctx, domain.SearchInput{UserID: "owner", Query: "Harbor", TopN: 50}, SearchVariantFull)
	if err != nil {
		t.Fatal(err)
	}
	d := found.SeedAdmission
	if d == nil || d.Limit != 128 || d.Retained != 128 || d.EntityOnlyRetained != 1 || found.Components["source"].RelationExpansion <= 0 {
		t.Fatalf("entity route or graph space starved: %+v %+v", d, found.Components["source"])
	}
	if found.BaseScores["posting"] != .65 {
		t.Fatal("admission inflated the entity score")
	}
}

func TestBalancedSeedChannelsKeepOrderBudgetAndOverlappingSignals(t *testing.T) {
	ordered := []string{"both", "dense", "lexical", "entity", "tail"}
	dense := map[string]float64{"both": .9, "dense": .8}
	lexical := map[string]float64{"both": .95, "lexical": .9}
	entity := map[string]float64{"entity": .2}
	got := balanceSeedChannels(ordered, 3, dense, lexical, entity)
	if len(got) != len(ordered) || got[0] != "both" || got[1] != "entity" || got[2] != "dense" {
		t.Fatalf("overlap consumed extra quota or entity channel was lost: %v", got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatal("duplicate seed", id)
		}
		seen[id] = true
	}
	if ordered[1] != "dense" || entity["entity"] != .2 {
		t.Fatal("input order or scores mutated")
	}
}

func TestSeedDiagnosticsDistinguishEntityMatchesFromAdmittedEvidence(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	for _, doc := range []domain.MemoryDocument{
		{UserID: "owner", ID: "a", NodeID: "a", Type: domain.MemoryTypeFact, Content: "A direct result."},
		{UserID: "owner", ID: "b", NodeID: "b", Type: domain.MemoryTypeFact, Content: "Another direct result."},
		{UserID: "owner", ID: "mention", NodeID: "mention", Type: domain.MemoryTypeEntity, Content: "Harbor", Metadata: map[string]any{"canonical_name": "Harbor", "interpretation": "source_mention"}},
	} {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	e := NewEngine(entityAdmissionRepo{repo}, directRecallVector{search: func(context.Context) ([]domain.VectorSearchResult, error) {
		return []domain.VectorSearchResult{{NodeID: "a", Score: .95}, {NodeID: "b", Score: .9}}, nil
	}})
	e.SetEntityRecall(queryExtractorFunc(func(context.Context, string, string) (domain.QueryEntities, error) {
		return domain.QueryEntities{Names: []string{"Harbor"}}, nil
	}), time.Second)
	for _, limit := range []int{2, 3} {
		e.SetRelationRecallConfig(RelationRecallConfig{MaxNodes: limit})
		found, err := e.SearchWithVariant(ctx, domain.SearchInput{UserID: "owner", Query: "Harbor", TopN: 3}, SearchVariantFullNoRelations)
		if err != nil {
			t.Fatal(err)
		}
		d := found.SeedAdmission
		if found.Result.EntityRecall.MatchedNodes != 1 || d == nil || d.Candidates != 3 || d.Retained != limit || d.EntityCandidates != 1 || d.EntityOnlyCandidates != 1 || d.EntityRetained != limit-2 || d.EntityOnlyRetained != limit-2 {
			t.Fatalf("match count incorrectly implies admitted evidence: entity=%+v seed=%+v", found.Result.EntityRecall, d)
		}
		if _, ok := found.BaseScores["mention"]; ok != (limit == 3) {
			t.Fatal("diagnostic changed seed selection")
		}
	}
}
