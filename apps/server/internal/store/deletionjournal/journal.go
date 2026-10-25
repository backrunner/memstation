// Package deletionjournal stores independently durable, authenticated deletion
// intents. Its directory must be outside the database backup/restore volume.
package deletionjournal

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"memstation/apps/server/internal/domain"

	"github.com/google/uuid"
)

type Journal struct {
	path string
	key  []byte
}
type header struct {
	Version      int    `json:"version"`
	DeploymentID string `json:"deployment_id"`
	MAC          string `json:"mac"`
}
type entry struct {
	domain.DeletionEntry
	Previous string `json:"previous"`
}

func New(path, secret string) (*Journal, error) {
	if path == "" || secret == "" {
		return nil, errors.New("deletion journal configuration required")
	}
	key := sha256.Sum256([]byte("memstation/deletion-journal/v1\x00" + secret))
	return &Journal{path: path, key: key[:]}, nil
}

func (j *Journal) sign(v any) string {
	raw, _ := json.Marshal(v)
	mac := hmac.New(sha256.New, j.key)
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func (j *Journal) open(create bool) (*os.File, error) {
	if create {
		if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
			return nil, err
		}
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(j.path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
func closeFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }
func (j *Journal) read(f *os.File) (string, []domain.DeletionEntry, error) {
	stat, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	if stat.Size() == 0 {
		return "", nil, errors.New("deletion journal is empty")
	}
	var ending [1]byte
	if _, err = f.ReadAt(ending[:], stat.Size()-1); err != nil {
		return "", nil, err
	}
	if ending[0] != '\n' {
		return "", nil, errors.New("deletion journal has an incomplete durable record")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", nil, err
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", nil, err
		}
		return "", nil, errors.New("deletion journal header missing")
	}
	var h header
	if err := json.Unmarshal(scanner.Bytes(), &h); err != nil {
		return "", nil, err
	}
	signed := h.MAC
	h.MAC = ""
	if h.Version != 1 || h.DeploymentID == "" || !hmac.Equal([]byte(signed), []byte(j.sign(h))) {
		return "", nil, errors.New("deletion journal header authentication failed")
	}
	previous := signed
	entries := []domain.DeletionEntry{}
	for scanner.Scan() {
		var e entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return "", nil, err
		}
		digest := e.Digest
		e.Digest = ""
		if e.Sequence != int64(len(entries)+1) || e.Previous != previous || !hmac.Equal([]byte(digest), []byte(j.sign(e))) {
			return "", nil, errors.New("deletion journal continuity verification failed")
		}
		e.Digest = digest
		previous = digest
		entries = append(entries, e.DeletionEntry)
	}
	if err := scanner.Err(); err != nil {
		return "", nil, err
	}
	return h.DeploymentID, entries, nil
}

func (j *Journal) Initialize(ctx context.Context, expected string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := j.open(expected == "")
	if err != nil {
		return "", err
	}
	defer closeFile(f)
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	if stat.Size() == 0 {
		if expected != "" {
			return "", errors.New("required deletion journal missing")
		}
		h := header{Version: 1, DeploymentID: uuid.NewString()}
		h.MAC = j.sign(h)
		raw, err := json.Marshal(h)
		if err != nil {
			return "", err
		}
		if _, err = f.Write(append(raw, '\n')); err != nil {
			return "", err
		}
		if err = f.Sync(); err != nil {
			return "", err
		}
		directory, err := os.Open(filepath.Dir(j.path))
		if err != nil {
			return "", err
		}
		err = directory.Sync()
		_ = directory.Close()
		if err != nil {
			return "", err
		}
	}
	id, _, err := j.read(f)
	if err != nil {
		return "", err
	}
	if expected != "" && expected != id {
		return "", errors.New("deletion journal belongs to another deployment")
	}
	return id, nil
}

func (j *Journal) Entries(ctx context.Context) (string, []domain.DeletionEntry, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	f, err := j.open(false)
	if err != nil {
		return "", nil, err
	}
	defer closeFile(f)
	return j.read(f)
}

func (j *Journal) Append(ctx context.Context, intent domain.DeletionIntent) (domain.DeletionEntry, error) {
	var result domain.DeletionEntry
	if err := ctx.Err(); err != nil {
		return result, err
	}
	f, err := j.open(false)
	if err != nil {
		return result, err
	}
	defer closeFile(f)
	_, entries, err := j.read(f)
	if err != nil {
		return result, err
	}
	for _, prior := range entries {
		if prior.Intent.OperationID == intent.OperationID {
			sameRequest := intent.RequestHash != "" && prior.Intent.RequestHash == intent.RequestHash && prior.Intent.UserID == intent.UserID && prior.Intent.WorkspaceID == intent.WorkspaceID && prior.Intent.PartitionID == intent.PartitionID
			if !sameRequest && j.sign(prior.Intent) != j.sign(intent) {
				return result, errors.New("deletion intent conflicts with durable journal")
			}
			return prior, nil
		}
	}
	previous := ""
	if len(entries) > 0 {
		previous = entries[len(entries)-1].Digest
	} else {
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return result, err
		}
		var h header
		if err = json.NewDecoder(f).Decode(&h); err != nil {
			return result, err
		}
		previous = h.MAC
	}
	e := entry{DeletionEntry: domain.DeletionEntry{Sequence: int64(len(entries) + 1), Intent: intent}, Previous: previous}
	e.Digest = j.sign(e)
	raw, err := json.Marshal(e)
	if err != nil {
		return result, err
	}
	if _, err = f.Seek(0, io.SeekEnd); err != nil {
		return result, err
	}
	if _, err = f.Write(append(raw, '\n')); err != nil {
		return result, err
	}
	if err = f.Sync(); err != nil {
		return result, fmt.Errorf("persist deletion journal: %w", err)
	}
	return e.DeletionEntry, nil
}
