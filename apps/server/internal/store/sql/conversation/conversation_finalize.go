package conversation

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"
)

var errOutboxStoreMismatch = errors.New("conversation and outbox must share a store")

func (r *HybridConversationRepository) RequestFinalization(ctx context.Context, input domain.ConversationFinalization, outbox ports.OutboxRepository) error {
	if db := dbutilstore.CurrentDB(r.db); db != nil {
		return NewPostgresConversationRepository(db).RequestFinalization(ctx, input, outbox)
	}
	return r.fallback.RequestFinalization(ctx, input, outbox)
}

func (r *InMemoryConversationRepository) RequestFinalization(ctx context.Context, input domain.ConversationFinalization, outbox ports.OutboxRepository) error {
	workspace, err := security.DataScope(ctx, input.UserID, input.WorkspaceID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.conversations[recordutilstore.Key(input.UserID, input.ConversationID)]
	if !ok || c.WorkspaceID != workspace {
		return domain.ErrConversationNotFound
	}
	if err = authorizeConversationWriter(ctx, c); err != nil {
		return err
	}
	if c.FinalizeStatus != domain.FinalizeStatusActive {
		return nil
	}
	if input.TimeoutBefore != nil && !c.UpdatedAt.Before(*input.TimeoutBefore) {
		return nil
	}
	update, event, err := finalizationEvent(c, input)
	if err != nil {
		return err
	}
	if event != nil {
		var target *InMemoryOutboxRepository
		switch value := outbox.(type) {
		case *InMemoryOutboxRepository:
			target = value
		case *HybridOutboxRepository:
			if dbutilstore.CurrentDB(value.db) == nil {
				target = value.fallback
			}
		}
		if target == nil {
			return errOutboxStoreMismatch
		}
		target.mu.Lock()
		defer target.mu.Unlock()
		if old, exists := target.events[event.ID]; exists {
			if old.UserID != c.UserID || old.AggregateID != c.ConversationID {
				return domain.ErrAccessConflict
			}
		} else {
			target.events[event.ID] = *event
		}
	}
	r.conversations[recordutilstore.Key(input.UserID, input.ConversationID)] = applyConversationState(c, update)
	return nil
}

func (r *PostgresConversationRepository) RequestFinalization(ctx context.Context, input domain.ConversationFinalization, outbox ports.OutboxRepository) error {
	workspace, err := security.DataScope(ctx, input.UserID, input.WorkspaceID)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "conversation:"+input.ConversationID); err != nil {
		return err
	}
	c := domain.ConversationRecord{UserID: input.UserID, ConversationID: input.ConversationID, WorkspaceID: workspace}
	var metadata []byte
	err = tx.QueryRowContext(ctx, `SELECT agent_id,write_revision,finalize_status,metadata,updated_at FROM conversations WHERE user_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE`, input.UserID, workspace, input.ConversationID).Scan(&c.AgentID, &c.Revision, &c.FinalizeStatus, &metadata, &c.UpdatedAt)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.ErrConversationNotFound
	}
	if err != nil {
		return err
	}
	if err = authorizeConversationWriter(ctx, c); err != nil {
		return err
	}
	if c.FinalizeStatus != domain.FinalizeStatusActive {
		return nil
	}
	if input.TimeoutBefore != nil && !c.UpdatedAt.Before(*input.TimeoutBefore) {
		return nil
	}
	if len(metadata) > 0 {
		if err = json.Unmarshal(metadata, &c.Metadata); err != nil {
			return err
		}
	}
	update, event, err := finalizationEvent(c, input)
	if err != nil {
		return err
	}
	if event != nil {
		var target *sqlpkg.DB
		switch value := outbox.(type) {
		case *PostgresOutboxRepository:
			target = value.db
		case *HybridOutboxRepository:
			target = dbutilstore.CurrentDB(value.db)
		}
		if target != r.db {
			return errOutboxStoreMismatch
		}
		// Never reset a processed event on a duplicate request.
		_, err = tx.ExecContext(ctx, `INSERT INTO outbox_events (id,user_id,event_type,aggregate_type,aggregate_id,payload,status,available_at,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,'pending',$7,$7,$7) ON CONFLICT (id) DO NOTHING`, event.ID, event.UserID, event.EventType, event.AggregateType, event.AggregateID, dbutilstore.MarshalJSON(event.Payload), event.CreatedAt)
		if err != nil {
			return err
		}
		var owner, aggregate string
		if err = tx.QueryRowContext(ctx, `SELECT user_id,aggregate_id FROM outbox_events WHERE id=$1`, event.ID).Scan(&owner, &aggregate); err != nil {
			return err
		}
		if owner != c.UserID || aggregate != c.ConversationID {
			return domain.ErrAccessConflict
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE conversations SET finalize_status=$1,summary_status=COALESCE($2,summary_status),analysis_status=COALESCE($3,analysis_status),metadata=COALESCE(metadata,'{}'::jsonb)||$4::jsonb,updated_at=$5 WHERE id=$6 AND user_id=$7 AND workspace_id=$8`, string(*update.FinalizeStatus), nullableProcessingStatus(update.SummaryStatus), nullableProcessingStatus(update.AnalysisStatus), string(dbutilstore.MarshalJSON(update.Metadata)), update.UpdatedAt, c.ConversationID, c.UserID, workspace)
	if err != nil {
		return err
	}
	return tx.Commit()
}
