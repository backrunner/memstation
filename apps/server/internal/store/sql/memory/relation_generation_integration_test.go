package memory

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"memstation/apps/server/internal/domain"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRelationGenerationIsolation(t *testing.T) {
	databaseURL := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	const userID = "relation-generation-integration"
	if _, err = db.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,capabilities) VALUES ('personal-' || encode(sha256(convert_to($1,'UTF8')),'hex'),$1,'Fixture','[]') ON CONFLICT DO NOTHING`, userID); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM memory_relations WHERE user_id=$1`, userID)
		_, _ = db.ExecContext(ctx, `DELETE FROM memory_nodes WHERE user_id=$1`, userID)
	}
	cleanup()
	defer cleanup()
	for id, generation := range map[string]string{"rg-key": "", "rg-a": "g1", "rg-b": "g1", "rg-other": "g2"} {
		kind := "fact"
		if generation == "" {
			kind = "key_memory"
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO memory_nodes (workspace_id, id,generation_id,user_id,node_type,entity_id,embedding_status) VALUES ('personal-' || encode(sha256(convert_to($3,'UTF8')),'hex'),$1,$2,$3,$4,$1,'ready')`, id, generation, userID, kind); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewPostgresMemoryRepository(db)
	for _, endpoints := range [][2]string{{"rg-key", "rg-a"}, {"rg-b", "rg-key"}, {"rg-a", "rg-b"}} {
		if err = repo.LinkDocuments(ctx, domain.MemoryRelation{UserID: userID, SrcNodeID: endpoints[0], DstNodeID: endpoints[1], RelationType: "related_to", Weight: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range []domain.MemoryRelation{
		{UserID: userID, SrcNodeID: "rg-a", DstNodeID: "rg-other", RelationType: "related_to", Weight: 1},
		{UserID: userID, GenerationID: "g2", SrcNodeID: "rg-key", DstNodeID: "rg-a", RelationType: "related_to", Weight: 1},
		{UserID: "other-user", SrcNodeID: "rg-key", DstNodeID: "rg-a", RelationType: "related_to", Weight: 1},
	} {
		if err = repo.LinkDocuments(ctx, relation); err == nil {
			t.Fatalf("invalid relation accepted: %+v", relation)
		}
	}
	// Simulate an old conflicting row without deleting its audit history.
	if _, err = db.ExecContext(ctx, `INSERT INTO memory_relations (id,generation_id,user_id,src_node_id,dst_node_id,relation_type,weight) VALUES ('rg-legacy','g1',$1,'rg-a','rg-other','related_to',1)`, userID); err != nil {
		t.Fatal(err)
	}
	relations, err := repo.ListRelations(ctx, userID, "g1", []string{"rg-a", "rg-key"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(relations) != 3 {
		t.Fatalf("expected 3 scoped edges, got %+v", relations)
	}
	for _, relation := range relations {
		if relation.GenerationID != "g1" || relation.DstNodeID == "rg-other" {
			t.Fatalf("generation leak: %+v", relation)
		}
	}
	all, err := repo.ListRelations(ctx, userID, "", []string{"rg-a"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, relation := range all {
		if relation.ID == "rg-legacy" {
			return
		}
	}
	t.Fatal("legacy edge was lost instead of isolated")
}
