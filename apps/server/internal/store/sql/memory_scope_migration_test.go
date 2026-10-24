package sql

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"

	"github.com/google/uuid"
)

func TestMemoryScopeMigrationPreservesParentsAndRejectsAmbiguousVectors(t *testing.T) {
	url := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	schema := "memory_scope_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema+`; SET LOCAL search_path TO `+schema+`;
CREATE TABLE workspaces(id text PRIMARY KEY,user_id text,name text,capabilities jsonb,UNIQUE(user_id,id));
CREATE TABLE memory_nodes(id text PRIMARY KEY,user_id text,node_type text,activity_at timestamptz,UNIQUE(user_id,id));
CREATE TABLE memory_embeddings(embedding_id text PRIMARY KEY,user_id text,node_id text,generation_id text,embedding_version int,embedding_model text);
INSERT INTO memory_nodes(id,user_id,node_type) VALUES ('node','owner-一','fact');
INSERT INTO memory_embeddings VALUES ('vector','owner-一','node','gen',1,'fixture');
SAVEPOINT invalid_vector;
INSERT INTO memory_embeddings VALUES ('orphan','other','node','gen',1,'fixture');`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "ent", "migrate", "migrations", "202610240003_memory_node_scope.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, string(raw)); err == nil {
		t.Fatal("cross-owner vector was reassigned")
	}
	if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT invalid_vector`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	var nodeScope, vectorScope string
	if err = tx.QueryRowContext(ctx, `SELECT n.workspace_id,e.workspace_id FROM memory_nodes n JOIN memory_embeddings e ON e.node_id=n.id`).Scan(&nodeScope, &vectorScope); err != nil || nodeScope != domain.PersonalWorkspaceID("owner-一") || vectorScope != nodeScope {
		t.Fatalf("scope backfill: %s %s %v", nodeScope, vectorScope, err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces VALUES ('group','owner-一','Group','[]')`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE memory_nodes SET workspace_id='group' WHERE id='node'`,
		`UPDATE memory_embeddings SET workspace_id='group' WHERE embedding_id='vector'`,
		`INSERT INTO memory_embeddings(embedding_id,user_id,workspace_id,node_id) VALUES ('wrong','owner-一','group','node')`,
	} {
		if _, err = tx.ExecContext(ctx, `SAVEPOINT immutable_scope`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, query); err == nil {
			t.Fatalf("scope mutation accepted: %s", query)
		}
		if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT immutable_scope`); err != nil {
			t.Fatal(err)
		}
	}
}
