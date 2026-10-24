package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

type LocalStore struct {
	root string
}

func NewStore(cfg bootstrap.MultimodalConfig) (ports.BlobStore, error) {
	backend := strings.TrimSpace(strings.ToLower(cfg.StorageBackend))
	switch backend {
	case "", "local":
		root := strings.TrimSpace(cfg.StorageLocalRoot)
		if root == "" {
			root = "./data/blobs"
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
		return &LocalStore{root: root}, nil
	default:
		return nil, fmt.Errorf("unsupported blob storage backend: %s", cfg.StorageBackend)
	}
}

func (s *LocalStore) Put(ctx context.Context, blob domain.BlobObject, body io.Reader) (domain.BlobObject, error) {
	workspace, err := security.PersonalScope(ctx, blob.UserID, blob.WorkspaceID)
	if err != nil {
		return domain.BlobObject{}, err
	}
	blob.WorkspaceID = workspace
	if blob.BlobID == "" || blob.DeletedAt != nil {
		return domain.BlobObject{}, domain.ErrAccessDenied
	}
	expected := scopedKey(blob)
	if blob.StorageKey != "" && blob.StorageKey != expected {
		return domain.BlobObject{}, domain.ErrAccessDenied
	}
	blob.StorageKey = expected
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return domain.BlobObject{}, err
	}
	defer root.Close()
	if err = root.MkdirAll(filepath.Dir(expected), 0o700); err != nil {
		return domain.BlobObject{}, err
	}
	// Temporary writes are not readable through a published key. Link publishes
	// atomically without replacing an existing blob or following a target link.
	temp := filepath.Join(filepath.Dir(expected), "upload-"+uuid.NewString())
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return domain.BlobObject{}, err
	}
	defer func() { _ = root.Remove(temp) }()
	_, copyErr := io.Copy(file, body)
	closeErr := file.Close()
	if err = errors.Join(copyErr, closeErr, ctx.Err()); err != nil {
		return domain.BlobObject{}, err
	}
	if err = root.Link(temp, expected); err != nil {
		return domain.BlobObject{}, err
	}
	return blob, nil
}

func (s *LocalStore) Open(ctx context.Context, blob domain.BlobObject) (io.ReadCloser, error) {
	if err := validateBlobKey(ctx, blob); err != nil {
		return nil, err
	}
	if blob.DeletedAt != nil {
		return nil, os.ErrNotExist
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(blob.StorageKey)
}

func (s *LocalStore) Delete(ctx context.Context, blob domain.BlobObject) error {
	if err := validateBlobKey(ctx, blob); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(blob.StorageKey); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func scopedKey(blob domain.BlobObject) string {
	return filepath.Join("v2", domain.PersonalWorkspaceID(blob.UserID), domain.AccessID("blob", blob.BlobID), "content")
}

func validateBlobKey(ctx context.Context, blob domain.BlobObject) error {
	if _, err := security.PersonalScope(ctx, blob.UserID, blob.WorkspaceID); err != nil {
		return err
	}
	if blob.BlobID == "" || blob.StorageKey == "" || !filepath.IsLocal(blob.StorageKey) || filepath.Clean(blob.StorageKey) != blob.StorageKey {
		return domain.ErrAccessDenied
	}
	if blob.StorageKey == scopedKey(blob) {
		return nil
	}
	// Existing on-disk uploads retain their original layout. Only an exact
	// owner/blob directory is accepted, never an arbitrary metadata path.
	if filepath.Base(blob.UserID) != blob.UserID || filepath.Base(blob.BlobID) != blob.BlobID || blob.UserID == "." || blob.UserID == ".." || blob.BlobID == "." || blob.BlobID == ".." {
		return domain.ErrAccessDenied
	}
	if filepath.Dir(blob.StorageKey) != filepath.Join(blob.UserID, blob.BlobID) {
		return domain.ErrAccessDenied
	}
	return nil
}
