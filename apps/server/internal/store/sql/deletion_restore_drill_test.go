package sql

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
)

// Explicit operational drill: actual pg_dump/pg_restore of a private test
// schema, with a newer independent deletion journal than the SQL backup.
func TestDeletionJournalActualBackupRestore(t *testing.T) {
	if os.Getenv("MEMSTATION_TEST_BACKUP_RESTORE") != "1" {
		t.Skip("manual backup/restore drill")
	}
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(tool, "required")
		}
	}
	db, repo, ingest, ctx, b := lifecycleFixture(t)
	receipt, err := ingest.Observe(ctx, b, "backup-source", 8192)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.SourceRef{WorkspaceID: b.WorkspaceID, MessageID: receipt.Events[0].MessageID, Revision: 1, PartIndex: 0}
	var schema string
	if err = db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil || !strings.HasPrefix(schema, "lifecycle_") {
		t.Fatal("isolated schema required", err)
	}
	u, err := url.Parse(os.Getenv("MEMSTATION_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	query := u.Query()
	query.Del("search_path")
	u.RawQuery = query.Encode()
	password, _ := u.User.Password()
	u.User = url.User(u.User.Username())
	backup := filepath.Join(t.TempDir(), "before-erase.dump")
	run := func(tool string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), tool, append([]string{"--dbname", u.String()}, args...)...)
		cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v\n%s", tool, err, output)
		}
	}
	run("pg_dump", "--format=custom", "--schema="+schema, "--no-owner", "--no-privileges", "--file="+backup)
	op, err := repo.Forget(ctx, domain.ForgetSelector{SourceRefs: []domain.SourceRef{ref}}, "after-backup-erase")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ProcessOperation(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
	run("pg_restore", "--clean", "--if-exists", "--no-owner", "--no-privileges", backup)
	// Prove the backup really contained the old body before applying the barrier.
	var old bool
	if err = db.QueryRow(`SELECT NOT tombstoned AND parts<>'[]'::jsonb FROM source_messages WHERE id=$1`, ref.MessageID).Scan(&old); err != nil || !old {
		t.Fatal("backup did not restore old source", err)
	}
	cleared := false
	repo.SetCleanup(nil, func(context.Context) error { cleared = true; return nil })
	if err = repo.EnsureDeletionJournal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Source(ctx, ref.MessageID); !errors.Is(err, domain.ErrAccessNotFound) {
		t.Fatal("backup resurrected source", err)
	}
	restored, err := repo.Operation(ctx, op.ID)
	if err != nil || !restored.ReadBlocked || !restored.OnlineCleaned || !restored.JournalPersisted || !cleared {
		t.Fatalf("recovery incomplete: %+v %v", restored, err)
	}
	if info, err := os.Stat(backup); err != nil || info.Size() == 0 {
		t.Fatal("backup artifact empty", err)
	} else {
		t.Logf("actual PostgreSQL restore: %d bytes; latest deletion journal replayed before readiness", info.Size())
	}
}
