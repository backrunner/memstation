package deletionjournal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

func TestJournalAuthenticatesOrderAndRequiresRetainedFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, err := New(path, "test-only-key")
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := j.Initialize(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	intent := domain.DeletionIntent{OperationID: "op", UserID: "owner", WorkspaceID: "group", SourceIDs: []string{"source"}, Cutoff: time.Now().UTC()}
	first, err := j.Append(ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	again, err := j.Append(ctx, intent)
	if err != nil || again.Digest != first.Digest || again.Sequence != 1 {
		t.Fatalf("duplicate %+v %v", again, err)
	}
	intent.SourceIDs = []string{"changed"}
	if _, err = j.Append(ctx, intent); err == nil {
		t.Fatal("conflicting intent accepted")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-6] ^= 1
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = j.Entries(ctx); err == nil {
		t.Fatal("corrupt journal accepted")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Initialize(ctx, deployment); err == nil {
		t.Fatal("missing retained journal silently replaced")
	}
}
