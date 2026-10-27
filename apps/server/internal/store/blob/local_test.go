package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func TestLocalBlobScopeAndAtomicPublication(t *testing.T) {
	root := t.TempDir()
	s := &LocalStore{root: root}
	user := "owner/with opaque components"
	ctx := security.WithAccess(context.Background(), domain.AccessContext{UserID: user, WorkspaceID: domain.PersonalWorkspaceID(user), ScopeVersion: 2, PartitionIDs: []string{""}})
	input := domain.BlobObject{UserID: user, BlobID: "blob:one", OriginalFilename: "../../secret"}
	b, err := s.Put(ctx, input, strings.NewReader("visible"))
	if err != nil {
		t.Fatal(err)
	}
	if b.WorkspaceID != domain.PersonalWorkspaceID(user) || b.StorageKey != scopedKey(b) {
		t.Fatal("scope was not materialized", b)
	}
	if _, err = s.Put(ctx, input, strings.NewReader("overwritten")); err == nil {
		t.Fatal("existing blob overwritten")
	}
	reader, err := s.Open(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(data) != "visible" {
		t.Fatal("published content changed", err)
	}
	for _, mutate := range []func(*domain.BlobObject){
		func(b *domain.BlobObject) { b.UserID = "other" },
		func(b *domain.BlobObject) { b.WorkspaceID = "group" },
		func(b *domain.BlobObject) { b.StorageKey = "../escape" },
		func(b *domain.BlobObject) { b.BlobID = "different-blob" },
	} {
		forged := b
		mutate(&forged)
		if _, err = s.Open(ctx, forged); !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatal("forged read accepted", err)
		}
		if err = s.Delete(ctx, forged); !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatal("forged deletion accepted", err)
		}
	}
	failed := input
	failed.BlobID = "failed"
	if _, err = s.Put(ctx, failed, io.MultiReader(strings.NewReader("partial"), brokenReader{})); err == nil {
		t.Fatal("partial copy accepted")
	}
	if _, err = os.Stat(filepath.Join(root, scopedKey(failed))); !os.IsNotExist(err) {
		t.Fatal("partial write published", err)
	}
	if err = s.Delete(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Open(ctx, b); !os.IsNotExist(err) {
		t.Fatal("deleted bytes readable", err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestBlobRejectsRetiredLayoutAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	s := &LocalStore{root: root}
	ctx := context.Background()
	b := domain.BlobObject{UserID: "owner", BlobID: "legacy", StorageKey: filepath.Join("owner", "legacy", "file.txt")}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(b.StorageKey)), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, b.StorageKey)
	if err := os.WriteFile(path, []byte("old bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := s.Open(ctx, b)
	if err == nil {
		f.Close()
		t.Fatal("retired blob layout accepted")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(outside, "private"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(outside, "private"), path); err != nil {
		t.Fatal(err)
	}
	if f, err = s.Open(ctx, b); err == nil {
		f.Close()
		t.Fatal("symlink escaped blob root")
	}
}
