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

func TestLifecycleScopeMigrationBackfillAndReferences(t *testing.T) {
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
	schema := "lifecycle_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema+`;SET LOCAL search_path TO `+schema+`;
 CREATE TABLE workspaces(id text PRIMARY KEY,user_id text,name text,capabilities jsonb,UNIQUE(user_id,id));
 CREATE TABLE conversations(id text PRIMARY KEY,user_id text,workspace_id text,UNIQUE(user_id,workspace_id,id));
 CREATE TABLE memory_generations(generation_id text PRIMARY KEY,user_id text,is_active bool DEFAULT false,created_at timestamptz);
 CREATE UNIQUE INDEX memory_generations_one_active_per_user_idx ON memory_generations(user_id) WHERE is_active;
 CREATE TABLE memory_source_snapshots(snapshot_id text PRIMARY KEY,user_id text,conversation_id text,snapshot_version int,created_at timestamptz);
 CREATE TABLE memory_source_messages(id text PRIMARY KEY,user_id text,snapshot_id text,conversation_id text,snapshot_version int);
 CREATE TABLE memory_generation_sources(id text PRIMARY KEY,user_id text,generation_id text,snapshot_id text,conversation_id text);
 CREATE TABLE blob_objects(blob_id text PRIMARY KEY,user_id text,created_at timestamptz);
 CREATE TABLE message_parts(part_id text PRIMARY KEY,user_id text,conversation_id text,blob_id text);
 CREATE TABLE message_part_artifacts(artifact_id text PRIMARY KEY,user_id text,conversation_id text,part_id text);
 CREATE FUNCTION memstation_immutable_memory_scope() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF (NEW.user_id,NEW.workspace_id) IS DISTINCT FROM (OLD.user_id,OLD.workspace_id) THEN RAISE EXCEPTION 'immutable'; END IF; RETURN NEW; END $$;
 INSERT INTO conversations VALUES('conv','用户-一','personal-' || encode(sha256(convert_to('用户-一','UTF8')),'hex'));
 INSERT INTO memory_generations(generation_id,user_id,is_active) VALUES('gen','用户-一',true);
 INSERT INTO memory_source_snapshots(snapshot_id,user_id,conversation_id,snapshot_version) VALUES('snap','用户-一','conv',1),('retained','用户-一','removed-conversation',1);
 INSERT INTO memory_source_messages VALUES('msg','用户-一','snap','conv',1);
 INSERT INTO memory_generation_sources VALUES('source','用户-一','gen','snap','conv');
 INSERT INTO blob_objects(blob_id,user_id) VALUES('blob','用户-一');
 INSERT INTO message_parts VALUES('part','用户-一','conv','blob');
 INSERT INTO message_part_artifacts VALUES('artifact','用户-一','conv','part');
 SAVEPOINT ambiguous;
 INSERT INTO memory_generation_sources VALUES('forged','other','gen','snap','conv');`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "ent", "migrate", "migrations", "202610240004_generation_media_scope.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, string(raw)); err == nil {
		t.Fatal("ambiguous ownership reassigned")
	}
	if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT ambiguous`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"memory_generations", "memory_source_snapshots", "memory_source_messages", "memory_generation_sources", "blob_objects", "message_parts", "message_part_artifacts"} {
		var scope string
		if err = tx.QueryRowContext(ctx, `SELECT workspace_id FROM `+table+` LIMIT 1`).Scan(&scope); err != nil || scope != domain.PersonalWorkspaceID("用户-一") {
			t.Fatal(table, scope, err)
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces VALUES('group','用户-一','Group','[]'); INSERT INTO memory_generations(generation_id,user_id,workspace_id,is_active) VALUES('group-gen','用户-一','group',true)`); err != nil {
		t.Fatal("active generation is not per workspace", err)
	}
	for _, query := range []string{
		`UPDATE blob_objects SET workspace_id='group' WHERE blob_id='blob'`,
		`UPDATE memory_source_snapshots SET user_id='other' WHERE snapshot_id='snap'`,
		`INSERT INTO memory_generation_sources(id,user_id,workspace_id,generation_id,snapshot_id,conversation_id) VALUES('wrong','用户-一','group','group-gen','snap','conv')`,
		`INSERT INTO message_parts(part_id,user_id,workspace_id,conversation_id,blob_id) VALUES('wrong','用户-一','group','conv','blob')`,
	} {
		if _, err = tx.ExecContext(ctx, `SAVEPOINT invalid_reference`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, query); err == nil {
			t.Fatal("cross-scope reference accepted", query)
		}
		if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT invalid_reference`); err != nil {
			t.Fatal(err)
		}
	}
}
