package ingestion

import (
	"context"
	sqlpkg "database/sql"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	ingestionservice "memstation/apps/server/internal/service/ingestion"
	storeaccess "memstation/apps/server/internal/store/access"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"

	"github.com/google/uuid"
)

func (r *PostgresIngestionRepository) OpenWindow(ctx context.Context, in domain.ConversationWindowInput, key string) (domain.ConversationWindow, error) {
	var out domain.ConversationWindow
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer dbutilstore.Rollback(tx)
	b := domain.ObserveBatch{WorkspaceID: in.WorkspaceID, IntegrationID: in.IntegrationID, ExternalSpaceID: in.ExternalSpaceID, ThreadID: in.ThreadID, PartitionID: in.PartitionID}
	user, _, err := ingestionAuthorityTx(ctx, tx, b)
	if err != nil {
		return out, err
	}
	if err = LockLifecycleScopeTx(ctx, tx, user, in.WorkspaceID, in.PartitionID); err != nil {
		return out, err
	}
	stream := dbutilstore.IngestionID("stream", user, in.WorkspaceID, in.IntegrationID, in.ExternalSpaceID, in.ThreadID, in.PartitionID)
	request := dbutilstore.IngestionID("window-request", stream, key)
	var conversation, hash string
	err = tx.QueryRowContext(ctx, `SELECT conversation_id,payload_hash FROM event_conversation_requests WHERE id=$1`, request).Scan(&conversation, &hash)
	if err == nil {
		if hash != IngestionHash(in) {
			return out, ingestionservice.ErrIdempotencyConflict
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		return r.Window(ctx, conversation)
	}
	if !errors.Is(err, sqlpkg.ErrNoRows) {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO memory_streams(id,user_id,workspace_id,integration_id,external_space_id,thread_id,partition_id) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, stream, user, in.WorkspaceID, in.IntegrationID, in.ExternalSpaceID, in.ThreadID, in.PartitionID); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT c.id FROM memory_streams s JOIN conversations c ON c.id=s.current_conversation_id WHERE s.id=$1 AND c.finalize_status='active' FOR UPDATE OF s,c`, stream).Scan(&conversation)
	if err != nil && !errors.Is(err, sqlpkg.ErrNoRows) {
		return out, err
	}
	if conversation == "" {
		conversation = uuid.NewString()
		a, _ := security.AccessFromContext(ctx)
		metadata := map[string]any{"stream_id": stream, "message_count": 0, "input_budget_units": 0, "partition_id": in.PartitionID, "memory_persist_enabled": true, "source_retain_enabled": true}
		if _, err = tx.ExecContext(ctx, `INSERT INTO conversations(id,user_id,workspace_id,agent_id,partition_id,context_mode,request_format,mode,finalize_status,summary_status,analysis_status,metadata) VALUES($1,$2,$3,$4,$5,'events','events','memory','active','pending','pending',$6)`, conversation, user, in.WorkspaceID, a.AgentID, in.PartitionID, dbutilstore.MarshalJSON(metadata)); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE memory_streams SET current_conversation_id=$2 WHERE id=$1`, stream, conversation); err != nil {
			return out, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO event_conversation_requests(id,user_id,workspace_id,partition_id,stream_id,conversation_id,payload_hash) VALUES($1,$2,$3,$4,$5,$6,$7)`, request, user, in.WorkspaceID, in.PartitionID, stream, conversation, IngestionHash(in)); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return r.Window(ctx, conversation)
}

func (r *PostgresIngestionRepository) Window(ctx context.Context, id string) (domain.ConversationWindow, error) {
	var out domain.ConversationWindow
	a, ok := security.AccessFromContext(ctx)
	if !ok {
		return out, domain.ErrAccessDenied
	}
	err := r.db.QueryRowContext(ctx, `SELECT id,workspace_id,partition_id,COALESCE(metadata->>'stream_id',''),write_revision,finalize_status FROM conversations WHERE user_id=$1 AND workspace_id=$2 AND id=$3 AND context_mode='events' AND `+storeaccess.PartitionSQL(ctx, "partition_id"), a.UserID, a.WorkspaceID, id).Scan(&out.ID, &out.WorkspaceID, &out.PartitionID, &out.StreamID, &out.Revision, &out.Status)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		err = domain.ErrAccessNotFound
	}
	return out, err
}
