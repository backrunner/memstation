package sql

import (
	"context"
	"database/sql"
	"time"

	"memstation/apps/server/internal/domain"

	"github.com/google/uuid"
)

func lockKeyMemoryEditTx(ctx context.Context, tx *sql.Tx, user, key string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "key-memory:"+user+":"+key)
	return err
}

func memoryEditEvent(user, action, kind, id string, condition *domain.MemoryWriteCondition) domain.AuditEvent {
	return domain.AuditEvent{ID: uuid.NewString(), ActorType: "admin", ActorID: "admin", Action: action, TargetType: kind, TargetID: id, CreatedAt: time.Now().UTC(), Payload: map[string]any{"user_id": user, "workspace_id": domain.PersonalWorkspaceID(user), "generation_id": condition.GenerationID, "expected_updated_at": condition.UpdatedAt}}
}

func memoryEditAuditTx(ctx context.Context, tx *sql.Tx, user, action, kind, id string, condition *domain.MemoryWriteCondition) error {
	e := memoryEditEvent(user, action, kind, id, condition)
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events (id,actor_type,actor_id,action,target_type,target_id,payload,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)`, e.ID, e.ActorType, e.ActorID, e.Action, e.TargetType, e.TargetID, marshalJSON(e.Payload), e.CreatedAt)
	return err
}

func (r *HybridMemoryRepository) UpdateDocumentConditional(ctx context.Context, user, id string, kind domain.MemoryType, content string, condition domain.MemoryWriteCondition) (domain.MemoryDocument, error) {
	if db := currentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).UpdateDocumentConditional(ctx, user, id, kind, content, condition)
	}
	return r.fallback.UpdateDocumentConditional(ctx, user, id, kind, content, condition)
}

func (r *HybridMemoryRepository) UpsertKeyMemoryConditional(ctx context.Context, km domain.KeyMemory, condition domain.MemoryWriteCondition) error {
	if db := currentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).UpsertKeyMemoryConditional(ctx, km, condition)
	}
	return r.fallback.UpsertKeyMemoryConditional(ctx, km, condition)
}

func (r *HybridMemoryRepository) DeleteKeyMemoryConditional(ctx context.Context, user, key string, condition domain.MemoryWriteCondition) error {
	if db := currentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).DeleteKeyMemoryConditional(ctx, user, key, condition)
	}
	return r.fallback.DeleteKeyMemoryConditional(ctx, user, key, condition)
}

func (r *HybridMemoryRepository) SetFallbackAudit(record func(context.Context, domain.AuditEvent)) {
	r.fallback.fallbackAudit = record
}

func (r *InMemoryMemoryRepository) editAudit(ctx context.Context, user, action, kind, id string, condition *domain.MemoryWriteCondition) {
	if condition == nil {
		return
	}
	e := memoryEditEvent(user, action, kind, id, condition)
	r.editAudits = append(r.editAudits, e)
	if r.fallbackAudit != nil {
		r.fallbackAudit(ctx, e)
	}
}
