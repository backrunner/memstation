package blob

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
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

func (s *LocalStore) Put(_ context.Context, blob domain.BlobObject, body io.Reader) (domain.BlobObject, error) {
	if blob.StorageKey == "" {
		filename := strings.TrimSpace(blob.OriginalFilename)
		if filename == "" {
			filename = blob.BlobID
		}
		blob.StorageKey = filepath.Join(blob.UserID, blob.BlobID, filepath.Base(filename))
	}
	path := filepath.Join(s.root, filepath.Clean(blob.StorageKey))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return domain.BlobObject{}, err
	}
	file, err := os.Create(path)
	if err != nil {
		return domain.BlobObject{}, err
	}
	defer file.Close()
	if _, err := io.Copy(file, body); err != nil {
		return domain.BlobObject{}, err
	}
	return blob, nil
}

func (s *LocalStore) Open(_ context.Context, blob domain.BlobObject) (io.ReadCloser, error) {
	return os.Open(filepath.Join(s.root, filepath.Clean(blob.StorageKey)))
}

func (s *LocalStore) Delete(_ context.Context, blob domain.BlobObject) error {
	if strings.TrimSpace(blob.StorageKey) == "" {
		return nil
	}
	if err := os.Remove(filepath.Join(s.root, filepath.Clean(blob.StorageKey))); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
