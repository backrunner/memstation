package vector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
)

func TestVectorScopeRejectsOwnerSubstitutionAndExactDeletes(t *testing.T) {
	s := NewPGVectorStore(nil)
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: "owner", WorkspaceID: domain.PersonalWorkspaceID("owner")})
	for _, input := range []domain.VectorSearchInput{{UserID: "other"}, {UserID: "owner", WorkspaceID: "group"}} {
		if _, err := s.Search(ctx, input); !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatalf("search ignored scope: %v", err)
		}
	}
	if err := s.Upsert(ctx, domain.VectorUpsertRequest{UserID: "owner", WorkspaceID: "group"}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("write ignored scope: %v", err)
	}
	if err := s.DeleteByNode(ctx, "other", "node"); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("delete ignored scope: %v", err)
	}
	for _, node := range []string{"node", "prefix::node::suffix"} {
		if err := s.Upsert(ctx, domain.VectorUpsertRequest{UserID: "owner", NodeID: node, NodeType: "fact", Vector: []float32{1, 0, 0}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteByNode(ctx, "owner", "node"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Search(ctx, domain.VectorSearchInput{UserID: "owner", Query: "test"})
	if err != nil || len(got) != 1 || got[0].NodeID != "prefix::node::suffix" {
		t.Fatalf("delete was not an exact identity match: %+v %v", got, err)
	}
}

func TestPostgresVectorScopeAndTypeFilterBeforeCandidateLimit(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner := "vector-scope-" + uuid.NewString()
	workspace := domain.PersonalWorkspaceID(owner)
	group := owner + "-group"
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: owner, WorkspaceID: workspace})
	if _, err = db.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,kind,capabilities) VALUES ($1,$2,'Personal','personal','[]'),($3,$2,'Group','group','[]')`, workspace, owner, group); err != nil {
		t.Fatal(err)
	}
	store := NewPGVectorStore(runtimeconfig.NewStaticDBHandle(db))
	queryVector := vectorLiteral(EmbedText("scope query"))
	for i := range 32 {
		id := fmt.Sprintf("%s-%d", owner, i)
		kind := "summary"
		scope := workspace
		if i == 0 {
			kind = "fact"
		}
		if i == 1 {
			scope = group
			kind = "fact"
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO memory_nodes(id,user_id,workspace_id,node_type,entity_id,embedding_status) VALUES ($1,$2,$3,$4,$1,'ready')`, id, owner, scope, kind); err != nil {
			t.Fatal(err)
		}
		vec := queryVector
		if i == 0 {
			vec = "[1,0,0]"
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO memory_embeddings(embedding_id,user_id,workspace_id,node_id,node_type,embedding_version,embedding_model,content_hash,vector) VALUES ($1,$2,$3,$1,$4,1,'fixture','hash',$5::vector)`, id, owner, scope, kind, vec); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Search(ctx, domain.VectorSearchInput{UserID: owner, Query: "scope query", TopK: 1, EmbeddingModel: "fixture", AllowedTypes: []domain.MemoryType{domain.MemoryTypeFact}})
	if err != nil || len(got) != 1 || got[0].NodeID != owner+"-0" || got[0].WorkspaceID != workspace {
		t.Fatalf("candidate filtering happened too late: %+v %v", got, err)
	}
	if err = store.Upsert(ctx, domain.VectorUpsertRequest{UserID: owner, NodeID: owner + "-1", NodeType: "fact", Vector: []float32{1, 0, 0}}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("cross-scope parent accepted: %v", err)
	}
	if err = store.DeleteByNode(ctx, owner, owner+"-1"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM memory_embeddings WHERE embedding_id=$1`, owner+"-1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("hidden embedding deleted: %d %v", count, err)
	}
	// A failure after vector insertion must roll back both the vector and ready state.
	if _, err = db.ExecContext(ctx, `UPDATE memory_nodes SET embedding_status='pending',embedding_revision=7 WHERE id=$1`, owner+"-0"); err != nil {
		t.Fatal(err)
	}
	constraint := "fail_ready_" + uuid.NewString()[:8]
	if _, err = db.ExecContext(ctx, `ALTER TABLE memory_nodes ADD CONSTRAINT `+constraint+` CHECK (id <> '`+owner+`-0' OR embedding_status <> 'ready') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	err = store.Upsert(ctx, domain.VectorUpsertRequest{UserID: owner, NodeID: owner + "-0", NodeType: "fact", EmbeddingModel: "fixture", EmbeddingRevision: 7, ContentHash: "must-rollback", Vector: []float32{0, 1, 0}})
	_, dropErr := db.ExecContext(ctx, `ALTER TABLE memory_nodes DROP CONSTRAINT `+constraint)
	if dropErr != nil {
		t.Fatal(dropErr)
	}
	if err == nil {
		t.Fatal("injected status failure was ignored")
	}
	var hash, status string
	if err = db.QueryRowContext(ctx, `SELECT e.content_hash,n.embedding_status FROM memory_embeddings e JOIN memory_nodes n ON n.id=e.node_id WHERE e.embedding_id=$1`, owner+"-0").Scan(&hash, &status); err != nil || hash != "hash" || status != "pending" {
		t.Fatalf("partial embedding commit: %s %s %v", hash, status, err)
	}
}
