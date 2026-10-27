package dbutil

import (
	"context"
	sqlpkg "database/sql"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	recordutilstore "memstation/apps/server/internal/store/sql/internal/recordutil"

	"github.com/google/uuid"
)

func InsertMemoryMutationOutboxTx(ctx context.Context, tx *sqlpkg.Tx, event domain.OutboxEvent) error {
	payload := recordutilstore.CloneMetadata(event.Payload)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["user_id"] = event.UserID
	payload["workspace_id"] = security.WorkspaceID(ctx, event.UserID)
	payload["partition_id"] = security.WritePartition(ctx)
	payload["schema_version"] = 2
	event.Payload = payload
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.AvailableAt.IsZero() {
		event.AvailableAt = event.CreatedAt
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (id, user_id, event_type, aggregate_type, aggregate_id, payload, status, available_at, processed_at, retry_count, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,'pending',$7,NULL,0,$8,$8)
		ON CONFLICT (id) DO UPDATE SET
			payload = EXCLUDED.payload,
			status = 'pending',
			available_at = EXCLUDED.available_at,
			processed_at = NULL,
			updated_at = EXCLUDED.updated_at
	`, event.ID, event.UserID, event.EventType, event.AggregateType, event.AggregateID, MarshalJSON(event.Payload), event.AvailableAt, event.CreatedAt)
	return err
}
