package memory

import (
	"context"
	"testing"
	"time"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

func TestHistoricalSearchDoesNotHydrateCurrentFact(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	for _, item := range []struct{ id, from, content string }{{"old", "2025-01-01T00:00:00Z", "Home: Hangzhou"}, {"new", "2026-03-01T00:00:00Z", "Home: Shanghai"}} {
		if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: item.id, NodeID: item.id, UserID: "u", Type: domain.MemoryTypeFact, Content: item.content, Metadata: map[string]any{"fact_key": "home", "valid_from": item.from}}); err != nil {
			t.Fatal(err)
		}
	}
	engine := NewEngine(repo, nil)
	service := NewService(engine, repo)
	at := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	result, err := service.Search(ctx, domain.SearchInput{UserID: "u", Query: "Home", AsOf: &at})
	if err != nil || len(result.Memories) != 1 || result.Memories[0].Content != "Home: Hangzhou" {
		t.Fatalf("historical result overwritten: %+v %v", result, err)
	}
	if len(result.Memories[0].Related) != 0 || result.Memories[0].Metadata["historical_snapshot"] != true {
		t.Fatal("historical result lost snapshot isolation")
	}
}

type timeoutGraphRepository struct{ ports.MemoryRepository }

func (r timeoutGraphRepository) RecallRelations(context.Context, domain.RelationQuery) ([]domain.MemoryRelation, error) {
	return nil, context.DeadlineExceeded
}

func TestAliasReviewAndGraphTimeoutPreserveDirectRecall(t *testing.T) {
	ctx := context.Background()
	repo := memorystore.NewInMemoryMemoryRepository()
	if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: "project", NodeID: "project", UserID: "u", Type: domain.MemoryTypeEntity, Content: "MemStation"}); err != nil {
		t.Fatal(err)
	}
	alias := domain.EntityAlias{UserID: "u", GenerationID: ("gen:current:" + domain.PersonalWorkspaceID("u")), Alias: "记忆站", Scope: "work", TargetNodeID: "project", Status: "candidate", Reason: "Explicit alternative name"}
	if _, err := repo.PutEntityAlias(ctx, alias); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(timeoutGraphRepository{repo}, nil)
	input := domain.SearchInput{UserID: "u", GenerationID: ("gen:current:" + domain.PersonalWorkspaceID("u")), Query: "记忆站", EntityScope: "work", TopN: 1}
	seeds := map[string]float64{}
	if err := engine.addEntitySeeds(ctx, input, seeds); err != nil || len(seeds) != 0 {
		t.Fatal("unreviewed alias affected retrieval", err)
	}
	alias.Status = "confirmed"
	if _, err := repo.PutEntityAlias(ctx, alias); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Search(ctx, input)
	if err != nil || len(result.Memories) != 1 || result.Memories[0].ID != "project" {
		t.Fatalf("direct recall lost on graph timeout: %+v %v", result, err)
	}
	if result.Memories[0].Metadata["graph_timeout"] != true {
		t.Fatal("graph timeout not reported")
	}
	input.EntityScope = "personal"
	seeds = map[string]float64{}
	if err := engine.addEntitySeeds(ctx, input, seeds); err != nil || len(seeds) != 0 {
		t.Fatal("alias leaked across context", err)
	}
	alias.Status = "revoked"
	if _, err := repo.PutEntityAlias(ctx, alias); err != nil {
		t.Fatal(err)
	}
	input.EntityScope = "work"
	if err := engine.addEntitySeeds(ctx, input, seeds); err != nil || len(seeds) != 0 {
		t.Fatal("revoked alias still participates", err)
	}
}
