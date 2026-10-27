package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"

	"github.com/google/uuid"
)

func TestGraphStorageContracts(t *testing.T) {
	t.Run("memory", func(t *testing.T) { graphStorageContract(t, NewInMemoryMemoryRepository()) })
	t.Run("postgres", func(t *testing.T) {
		url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
		if url == "" {
			t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
		}
		db, err := sql.Open("pgx", url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		graphStorageContract(t, NewPostgresMemoryRepository(db))
	})
}

func graphStorageContract(t *testing.T, r ports.MemoryRepository) {
	ctx := context.Background()
	u := "graph-contract-" + uuid.NewString()
	g := recordutilstore.CurrentGenerationID(domain.PersonalWorkspaceID(u))
	provisionFixtureGeneration(t, r, ctx, u, g)
	store := func(id string, kind domain.MemoryType, metadata map[string]any) {
		t.Helper()
		err := r.StoreDocument(ctx, domain.MemoryDocument{ID: u + id, NodeID: u + id, UserID: u, GenerationID: g, Type: kind, Content: id, Metadata: metadata, ActivityAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.DeleteDocument(ctx, u, u+id, kind) })
	}
	store("hub", domain.MemoryTypeEntity, nil)
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("n%02d", i)
		store(id, domain.MemoryTypeEntity, nil)
		if err := r.LinkDocuments(ctx, domain.MemoryRelation{UserID: u, GenerationID: g, SrcNodeID: u + "hub", DstNodeID: u + id, RelationType: domain.MemoryRelationDependsOn, Weight: float64(i+1) / 60}); err != nil {
			t.Fatal(err)
		}
	}
	edges, err := r.RecallRelations(ctx, domain.RelationQuery{UserID: u, GenerationID: g, NodeIDs: []string{u + "hub"}, MaxEdgesPerNode: 5, MaxEdges: 4, MaxNodes: 4, Mode: "dependencies"})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 3 {
		t.Fatalf("node budget: got %d edges", len(edges))
	}
	if edges[0].DstNodeID != u+"n59" {
		t.Fatalf("highest weight first: %+v", edges)
	}
	reverse, err := r.RecallRelations(ctx, domain.RelationQuery{UserID: u, GenerationID: g, NodeIDs: []string{u + "n59"}, Mode: "dependencies"})
	if err != nil || len(reverse) != 0 {
		t.Fatalf("wrong dependency direction: %+v %v", reverse, err)
	}
	reverse, err = r.RecallRelations(ctx, domain.RelationQuery{UserID: u, GenerationID: g, NodeIDs: []string{u + "n59"}, Mode: "dependents"})
	if err != nil || len(reverse) != 1 {
		t.Fatalf("dependents: %+v %v", reverse, err)
	}
	isolated, err := r.RecallRelations(ctx, domain.RelationQuery{UserID: u + "other", GenerationID: g, NodeIDs: []string{u + "hub"}})
	if err != nil || len(isolated) != 0 {
		t.Fatalf("user leakage: %v %v", isolated, err)
	}
	isolated, err = r.RecallRelations(ctx, domain.RelationQuery{UserID: u, GenerationID: g + "other", NodeIDs: []string{u + "hub"}})
	if err != nil || len(isolated) != 0 {
		t.Fatalf("generation leakage: %v %v", isolated, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = r.RecallRelations(cancelled, domain.RelationQuery{UserID: u, GenerationID: g, NodeIDs: []string{u + "hub"}}); err == nil {
		t.Fatal("cancelled traversal succeeded")
	}
	a := domain.EntityAlias{UserID: u, GenerationID: g, Alias: "Memory Station", TargetNodeID: u + "hub", SourceNodeID: u + "n00", Status: "candidate", Reason: "name mentioned in project scope"}
	a, err = r.PutEntityAlias(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	a.Status = "confirmed"
	a, err = r.PutEntityAlias(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	cycle := a
	cycle.Alias = "reverse"
	cycle.SourceNodeID = a.TargetNodeID
	cycle.TargetNodeID = a.SourceNodeID
	if _, err = r.PutEntityAlias(ctx, cycle); err == nil {
		t.Fatal("merge cycle accepted")
	}
	cross := a
	cross.UserID = u + "other"
	if _, err = r.PutEntityAlias(ctx, cross); err == nil {
		t.Fatal("cross-user alias accepted")
	}
	ambiguous := a
	ambiguous.TargetNodeID = u + "n01"
	ambiguous.SourceNodeID = ""
	if _, err = r.PutEntityAlias(ctx, ambiguous); err == nil {
		t.Fatal("ambiguous alias confirmed")
	}
	a.Status = "revoked"
	if _, err = r.PutEntityAlias(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err = r.GetDocument(ctx, u, u+"n00", domain.MemoryTypeEntity); err != nil {
		t.Fatal("revocation lost source node", err)
	}
	// Snapshot time is server-owned. A later correction must not alter known-at reads.
	store("fact-old", domain.MemoryTypeFact, map[string]any{"fact_key": "location", "valid_from": "2026-01-01T00:00:00Z"})
	known := time.Now().UTC()
	if pg, ok := r.(*PostgresMemoryRepository); ok {
		if err := pg.db.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&known); err != nil {
			t.Fatal(err)
		}
	}
	asOf := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	store("fact-new", domain.MemoryTypeFact, map[string]any{"fact_key": "location", "valid_from": "2026-03-01T00:00:00Z"})
	past, err := r.SearchFactsAt(ctx, domain.SearchInput{UserID: u, GenerationID: g, AsOf: &asOf})
	if err != nil || len(past) != 1 || past[0].ID != u+"fact-old" {
		t.Fatalf("historical validity: %+v %v", past, err)
	}
	asOf = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	past, err = r.SearchFactsAt(ctx, domain.SearchInput{UserID: u, GenerationID: g, AsOf: &asOf, KnownAt: &known})
	if err != nil || len(past) != 1 || past[0].ID != u+"fact-old" {
		t.Fatalf("known-at snapshot: %+v %v", past, err)
	}
	if _, err = r.UpdateDocument(ctx, u, u+"fact-new", domain.MemoryTypeFact, "corrected later"); err != nil {
		t.Fatal(err)
	}
	historical, err := r.SearchFactsAt(ctx, domain.SearchInput{UserID: u, GenerationID: g, AsOf: &asOf, KnownAt: &known})
	if err != nil || len(historical) != 1 || historical[0].Content == "corrected later" {
		t.Fatalf("future correction leaked: %+v %v", historical, err)
	}
	store("fact-late", domain.MemoryTypeFact, map[string]any{"fact_key": "location", "valid_from": "2026-02-01T00:00:00Z"})
	asOf = time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	late, err := r.SearchFactsAt(ctx, domain.SearchInput{UserID: u, GenerationID: g, AsOf: &asOf})
	if err != nil || len(late) != 1 || late[0].ID != u+"fact-late" {
		t.Fatalf("late-arriving fact lost its interval: %+v %v", late, err)
	}
	asOf = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	current, err := r.SearchFactsAt(ctx, domain.SearchInput{UserID: u, GenerationID: g, AsOf: &asOf})
	if err != nil || len(current) != 1 || current[0].ID != u+"fact-new" {
		t.Fatalf("late arrival replaced current state: %+v %v", current, err)
	}
	store("implicit", domain.MemoryTypeFact, nil)
	if _, err = r.UpdateDocument(ctx, u, u+"implicit", domain.MemoryTypeFact, "edited implicit validity"); err != nil {
		t.Fatal(err)
	}
	asOf = time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	snapshots, err := r.SearchFactsAt(ctx, domain.SearchInput{UserID: u, GenerationID: g, AsOf: &asOf})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range snapshots {
		if d.ID == u+"implicit" && d.Content == "edited implicit validity" {
			found = true
		}
	}
	if !found {
		t.Fatal("manual edit moved implicit fact validity to edit time")
	}
}
