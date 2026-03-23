package user

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Record struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email,omitempty"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

type Service struct {
	db *sqlpkg.DB

	mu      sync.RWMutex
	records map[string]Record
}

func NewService(db *sqlpkg.DB) *Service {
	svc := &Service{db: db, records: map[string]Record{}}
	svc.records["admin"] = Record{ID: "admin", Name: "Admin", IsAdmin: true, CreatedAt: time.Now().UTC()}
	return svc
}

func (s *Service) EnsureDefaultAdmin(ctx context.Context, name string) (Record, error) {
	record := Record{ID: "admin", Name: name, IsAdmin: true, CreatedAt: time.Now().UTC()}
	if s.db != nil {
		_, err := s.db.ExecContext(ctx, `
            INSERT INTO users (id, name, email, is_admin, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$5)
            ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, is_admin = EXCLUDED.is_admin, updated_at = EXCLUDED.updated_at
        `, record.ID, record.Name, "", true, record.CreatedAt)
		if err != nil {
			return Record{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.ID] = record
	return record, nil
}

func (s *Service) Create(ctx context.Context, name string, isAdmin bool) (Record, error) {
	record := Record{ID: uuid.NewString(), Name: name, IsAdmin: isAdmin, CreatedAt: time.Now().UTC()}
	if s.db != nil {
		_, err := s.db.ExecContext(ctx, `
            INSERT INTO users (id, name, email, is_admin, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$5)
        `, record.ID, record.Name, record.Email, record.IsAdmin, record.CreatedAt)
		if err != nil {
			return Record{}, err
		}
		return record, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.ID] = record
	return record, nil
}

func (s *Service) Get(ctx context.Context, id string) (Record, error) {
	if s.db != nil {
		var record Record
		if err := s.db.QueryRowContext(ctx, `SELECT id, name, email, is_admin, created_at FROM users WHERE id = $1`, id).Scan(&record.ID, &record.Name, &record.Email, &record.IsAdmin, &record.CreatedAt); err != nil {
			if errors.Is(err, sqlpkg.ErrNoRows) {
				return Record{}, errors.New("user not found")
			}
			return Record{}, err
		}
		return record, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[id]
	if !ok {
		return Record{}, errors.New("user not found")
	}
	return record, nil
}

func (s *Service) List(ctx context.Context) ([]Record, error) {
	if s.db != nil {
		rows, err := s.db.QueryContext(ctx, `SELECT id, name, email, is_admin, created_at FROM users ORDER BY created_at ASC`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]Record, 0)
		for rows.Next() {
			var item Record
			if err := rows.Scan(&item.ID, &item.Name, &item.Email, &item.IsAdmin, &item.CreatedAt); err != nil {
				return nil, err
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
