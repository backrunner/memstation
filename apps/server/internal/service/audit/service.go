package audit

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
)

type Service struct {
	db *sqlpkg.DB

	mu     sync.RWMutex
	events []domain.AuditEvent
}

type ListOptions struct {
	Limit      int
	ActorType  string
	Action     string
	TargetType string
	TargetID   string
}

func NewService(db *sqlpkg.DB) *Service { return &Service{db: db} }

func (s *Service) Record(ctx context.Context, event domain.AuditEvent) {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	if s.db != nil {
		_, err := s.db.ExecContext(ctx, `
            INSERT INTO audit_events (id, actor_type, actor_id, action, target_type, target_id, payload, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
        `,
			event.ID,
			event.ActorType,
			event.ActorID,
			event.Action,
			event.TargetType,
			event.TargetID,
			marshalJSON(event.Payload),
			event.CreatedAt,
		)
		if err == nil {
			return
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append([]domain.AuditEvent{event}, s.events...)
}

func (s *Service) List(ctx context.Context, limit int) []domain.AuditEvent {
	return s.ListFiltered(ctx, ListOptions{Limit: limit})
}

func (s *Service) ListFiltered(ctx context.Context, options ListOptions) []domain.AuditEvent {
	if s.db != nil {
		limit := options.Limit
		if limit <= 0 {
			limit = 100
		}
		query := `
            SELECT id, actor_type, actor_id, action, target_type, target_id, payload, created_at
            FROM audit_events
        `
		args := make([]any, 0, 5)
		clauses := make([]string, 0, 4)
		if value := strings.TrimSpace(options.ActorType); value != "" {
			args = append(args, value)
			clauses = append(clauses, fmt.Sprintf("actor_type = $%d", len(args)))
		}
		if value := strings.TrimSpace(options.Action); value != "" {
			args = append(args, value)
			clauses = append(clauses, fmt.Sprintf("action = $%d", len(args)))
		}
		if value := strings.TrimSpace(options.TargetType); value != "" {
			args = append(args, value)
			clauses = append(clauses, fmt.Sprintf("target_type = $%d", len(args)))
		}
		if value := strings.TrimSpace(options.TargetID); value != "" {
			args = append(args, value)
			clauses = append(clauses, fmt.Sprintf("target_id = $%d", len(args)))
		}
		if len(clauses) > 0 {
			query += " WHERE " + strings.Join(clauses, " AND ")
		}
		args = append(args, limit)
		query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err == nil {
			defer rows.Close()
			out := make([]domain.AuditEvent, 0, limit)
			for rows.Next() {
				var item domain.AuditEvent
				var payloadBytes []byte
				if err := rows.Scan(&item.ID, &item.ActorType, &item.ActorID, &item.Action, &item.TargetType, &item.TargetID, &payloadBytes, &item.CreatedAt); err != nil {
					out = nil
					break
				}
				_ = unmarshalJSON(payloadBytes, &item.Payload)
				out = append(out, item)
			}
			if out != nil && rows.Err() == nil {
				return out
			}
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := options.Limit
	if limit <= 0 || limit > len(s.events) {
		limit = len(s.events)
	}
	out := make([]domain.AuditEvent, 0, limit)
	for _, event := range s.events {
		if !matchesFilter(event, options) {
			continue
		}
		out = append(out, event)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func matchesFilter(event domain.AuditEvent, options ListOptions) bool {
	if value := strings.TrimSpace(options.ActorType); value != "" && event.ActorType != value {
		return false
	}
	if value := strings.TrimSpace(options.Action); value != "" && event.Action != value {
		return false
	}
	if value := strings.TrimSpace(options.TargetType); value != "" && event.TargetType != value {
		return false
	}
	if value := strings.TrimSpace(options.TargetID); value != "" && event.TargetID != value {
		return false
	}
	return true
}

func marshalJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func unmarshalJSON(raw []byte, value any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, value)
}
