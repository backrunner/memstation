package apikey

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/security"
)

type Record struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Hash      string     `json:"-"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type Service struct {
	db     *sqlpkg.DB
	hasher security.APIKeyHasher

	mu      sync.RWMutex
	records map[string]Record
}

func NewService(db *sqlpkg.DB) *Service {
	return &Service{db: db, records: map[string]Record{}}
}

func (s *Service) Create(ctx context.Context, userID, name string) (Record, string, error) {
	raw := "ms-" + uuid.NewString()
	prefix, hash, err := s.hasher.Hash(raw)
	if err != nil {
		return Record{}, "", err
	}
	record := Record{ID: uuid.NewString(), UserID: userID, Name: name, Prefix: prefix, Hash: hash, CreatedAt: time.Now().UTC()}
	if s.db != nil {
		_, err = s.db.ExecContext(ctx, `
            INSERT INTO api_keys (id, user_id, name, prefix, hash, scopes, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,'[]'::jsonb,$6,$6)
        `, record.ID, record.UserID, record.Name, record.Prefix, record.Hash, record.CreatedAt)
		if err != nil {
			return Record{}, "", err
		}
		return record, raw, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.ID] = record
	return record, raw, nil
}

func (s *Service) Revoke(ctx context.Context, id string) error {
	now := time.Now().UTC()
	if s.db != nil {
		_, err := s.db.ExecContext(ctx, `UPDATE api_keys SET revoked_at = $1, updated_at = $1 WHERE id = $2`, now, id)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return errors.New("api key not found")
	}
	record.RevokedAt = &now
	s.records[id] = record
	return nil
}

func (s *Service) LookupUserByHash(ctx context.Context, prefix, hash string) (string, error) {
	if s.db != nil {
		var userID string
		err := s.db.QueryRowContext(ctx, `
            SELECT user_id
            FROM api_keys
            WHERE prefix = $1 AND hash = $2 AND revoked_at IS NULL
            ORDER BY created_at DESC
            LIMIT 1
        `, prefix, hash).Scan(&userID)
		if err != nil {
			if errors.Is(err, sqlpkg.ErrNoRows) {
				return "", nil
			}
			return "", err
		}
		return userID, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.records {
		if record.Prefix == prefix && record.Hash == hash && record.RevokedAt == nil {
			return record.UserID, nil
		}
	}
	return "", nil
}

func (s *Service) List(ctx context.Context) ([]Record, error) {
	if s.db != nil {
		rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, prefix, hash, revoked_at, created_at FROM api_keys ORDER BY created_at DESC`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]Record, 0)
		for rows.Next() {
			var item Record
			var revokedAt sqlpkg.NullTime
			if err := rows.Scan(&item.ID, &item.UserID, &item.Name, &item.Prefix, &item.Hash, &revokedAt, &item.CreatedAt); err != nil {
				return nil, err
			}
			if revokedAt.Valid {
				item.RevokedAt = &revokedAt.Time
			}
			out = append(out, item)
		}
		return out, rows.Err()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0, len(s.records))
	for _, record := range s.records {
		out = append(out, record)
	}
	return out, nil
}

func PrefixAndHash(raw string) (string, string, error) {
	hasher := security.APIKeyHasher{}
	prefix, hash, err := hasher.Hash(raw)
	if err != nil {
		return "", "", err
	}
	if prefix == "" || hash == "" {
		return "", "", fmt.Errorf("invalid api key")
	}
	return prefix, hash, nil
}
