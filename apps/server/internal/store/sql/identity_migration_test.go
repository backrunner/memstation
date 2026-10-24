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

func TestPostgresIdentityBackfillPreservesLegacyOwnership(t *testing.T) {
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
	schema := "identity_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	// Every object below is in a new transaction-local test schema, rolled back.
	if _, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema+`; SET LOCAL search_path TO `+schema+`;
 CREATE TABLE users(id text);
 CREATE TABLE api_keys(id text,user_id text,prefix text,hash text,scopes jsonb,created_at timestamptz,revoked_at timestamptz,deleted_at timestamptz);
 CREATE TABLE conversations(id text,user_id text);
 CREATE TABLE memory_nodes(user_id text);
 INSERT INTO users VALUES ('owner-一');
 INSERT INTO api_keys VALUES ('old-key','owner-一','ms-old','unchanged-hash','["historically-unused"]',now(),now(),NULL);
 INSERT INTO conversations VALUES ('orphan-conversation','orphan-owner');
 INSERT INTO memory_nodes VALUES ('orphan-owner');`); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "ent", "migrate", "migrations", "202610240001_agent_identity.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, string(script)); err != nil {
		t.Fatal(err)
	}
	var agent, workspace, hash string
	var revoked bool
	if err = tx.QueryRowContext(ctx, `SELECT agent_id,default_workspace_id,hash,revoked_at IS NOT NULL FROM api_keys WHERE id='old-key'`).Scan(&agent, &workspace, &hash, &revoked); err != nil {
		t.Fatal(err)
	}
	if agent != domain.LegacyAgentID("owner-一") || workspace != domain.PersonalWorkspaceID("owner-一") || hash != "unchanged-hash" || !revoked {
		t.Fatal("legacy identity, hash or revocation changed")
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM workspaces`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("owner backfill: %d %v", count, err)
	}
	if _, err = tx.ExecContext(ctx, `SAVEPOINT owner_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE api_keys SET user_id='orphan-owner' WHERE id='old-key'`); err == nil {
		t.Fatal("composite ownership FK did not reject cross-owner credential")
	}
	if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT owner_check`); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE id='orphan-conversation' AND user_id='orphan-owner' AND write_revision=0`).Scan(&count); err != nil || count != 1 {
		t.Fatal("orphan history was reassigned or lost")
	}
}
