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

func TestPostgresConversationScopeMigration(t *testing.T) {
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
	schema := "conversation_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema+`; SET LOCAL search_path TO `+schema+`;
CREATE TABLE users(id text);
CREATE TABLE api_keys(id text,user_id text,prefix text,hash text,scopes jsonb,created_at timestamptz,revoked_at timestamptz,deleted_at timestamptz);
CREATE TABLE memory_nodes(user_id text);
CREATE TABLE conversations(id text PRIMARY KEY,user_id text,metadata jsonb,updated_at timestamptz DEFAULT now());
CREATE TABLE messages(id text PRIMARY KEY,user_id text,conversation_id text,sequence_num int);
CREATE TABLE token_usages(id text PRIMARY KEY,user_id text,conversation_id text);
INSERT INTO users VALUES ('owner-一');
INSERT INTO conversations(id,user_id,metadata) VALUES ('legacy','owner-一','{"agent_id":"new-agent","workspace_id":"forged"}'),('new','owner-一','{"agent_id":"new-agent"}'),('orphan','orphan-owner','{}');
INSERT INTO messages VALUES ('message','owner-一','legacy',0);
INSERT INTO token_usages VALUES ('usage','owner-一','legacy');`); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "ent", "migrate", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if _, err = tx.ExecContext(ctx, read("202610240001_agent_identity.sql")); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO actors(id,user_id,kind,display_name) VALUES ('new-actor','owner-一','agent','New agent');
INSERT INTO agents(id,user_id,actor_id,name,default_workspace_id) SELECT 'new-agent','owner-一','new-actor','New agent',id FROM workspaces WHERE user_id='owner-一';
UPDATE conversations SET write_revision=1 WHERE id='new';
UPDATE agents SET status='disabled' WHERE legacy;
SAVEPOINT orphan_check;
INSERT INTO messages VALUES ('invalid','different-owner','legacy',1);`); err != nil {
		t.Fatal(err)
	}
	migration := read("202610240002_conversation_scope.sql")
	if _, err = tx.ExecContext(ctx, migration); err == nil {
		t.Fatal("ambiguous child was silently reassigned")
	}
	if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT orphan_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, migration); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, user, agent string }{{"legacy", "owner-一", domain.LegacyAgentID("owner-一")}, {"new", "owner-一", "new-agent"}, {"orphan", "orphan-owner", domain.LegacyAgentID("orphan-owner")}} {
		var workspace, agent string
		if err = tx.QueryRowContext(ctx, `SELECT workspace_id,agent_id FROM conversations WHERE id=$1`, tc.id).Scan(&workspace, &agent); err != nil {
			t.Fatal(err)
		}
		if workspace != domain.PersonalWorkspaceID(tc.user) || agent != tc.agent {
			t.Fatalf("wrong scope backfill: %s %s %s", tc.id, workspace, agent)
		}
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM agents WHERE legacy AND status='active'`).Scan(&active); err != nil || active != 0 {
		t.Fatal("migration revived disabled Agents")
	}
	var childWorkspace string
	if err = tx.QueryRowContext(ctx, `SELECT workspace_id FROM messages WHERE id='message'`).Scan(&childWorkspace); err != nil || childWorkspace != domain.PersonalWorkspaceID("owner-一") {
		t.Fatal("message parent scope lost")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,kind,capabilities) VALUES ('group','owner-一','Group','group','[]')`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE conversations SET agent_id='new-agent' WHERE id='legacy'`,
		`UPDATE conversations SET workspace_id='group' WHERE id='legacy'`,
		`UPDATE messages SET conversation_id='new' WHERE id='message'`,
		`UPDATE token_usages SET user_id='orphan-owner' WHERE id='usage'`,
		`INSERT INTO messages(id,user_id,workspace_id,conversation_id,sequence_num) VALUES ('cross','owner-一','group','legacy',2)`,
	} {
		if _, err = tx.ExecContext(ctx, `SAVEPOINT scope_check`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, query); err == nil {
			t.Fatalf("ownership mutation accepted: %s", query)
		}
		if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT scope_check`); err != nil {
			t.Fatal(err)
		}
	}
}
