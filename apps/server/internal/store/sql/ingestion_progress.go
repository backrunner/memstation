package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
)

func (r *PostgresIngestionRepository) Progress(ctx context.Context, id string) (domain.IngestionReceipt, error) {
	var out domain.IngestionReceipt
	a, ok := security.AccessFromContext(ctx)
	if !ok {
		return out, domain.ErrAccessDenied
	}
	var events []byte
	var last int64
	err := r.db.QueryRowContext(ctx, `SELECT r.id,r.workspace_id,r.partition_id,r.stream_id,r.last_sequence,r.events,s.accepted_sequence,COALESCE((SELECT generation_id FROM memory_generations g WHERE g.user_id=s.user_id AND g.workspace_id=s.workspace_id AND g.is_active),s.generation_id) FROM ingestion_receipts r JOIN memory_streams s ON s.id=r.stream_id WHERE r.user_id=$1 AND r.workspace_id=$2 AND r.id=$3 AND `+storeaccess.PartitionSQL(ctx, "r.partition_id"), a.UserID, a.WorkspaceID, id).Scan(&out.ID, &out.WorkspaceID, &out.PartitionID, &out.StreamID, &last, &events, &out.AcceptedSequence, &out.GenerationID)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return out, domain.ErrAccessNotFound
	}
	if err != nil {
		return out, err
	}
	if out.GenerationID == "" {
		out.GenerationID = currentGenerationID(security.WorkspaceIdentity(ctx, a.UserID))
	}
	if err = json.Unmarshal(events, &out.Events); err != nil {
		return out, err
	}
	// A contiguous prefix, never MAX(completed sequence). Required terminal work
	// includes key reconciliation and all embeddings, even when extraction is empty.
	err = r.db.QueryRowContext(ctx, `SELECT COALESCE(MIN(e.sequence_num)-1,$2::bigint)
 FROM source_events e LEFT JOIN conversations c ON c.id=e.processing_conversation_id LEFT JOIN memory_operations o ON o.id=e.operation_id
 WHERE e.stream_id=$1 AND (
 (e.operation_id<>'' AND (o.id IS NULL OR NOT o.online_cleaned)) OR
 (e.processing_conversation_id<>'' AND (
  c.id IS NULL OR c.finalize_status NOT IN ('ended','timeout') OR
  EXISTS(SELECT 1 FROM memory_nodes n WHERE n.source_conversation_id=c.id AND ($3='' OR n.generation_id=$3) AND n.access_status='active' AND n.embedding_status NOT IN('completed','ready')) OR
  EXISTS(SELECT 1 FROM memory_jobs j WHERE j.conversation_id=c.id AND (j.generation_id='' OR j.generation_id=$3) AND j.status NOT IN('complete','skipped')) OR
  CASE WHEN NOT EXISTS(SELECT 1 FROM memory_generation_sources gs WHERE gs.conversation_id=c.id AND gs.generation_id=$3) THEN
   c.summary_status<>'completed' OR c.analysis_status<>'completed' OR NOT EXISTS(SELECT 1 FROM memory_jobs j WHERE j.conversation_id=c.id AND (j.generation_id='' OR j.generation_id=$3) AND j.task_type='keymemory.reconcile' AND j.status='complete')
  ELSE NOT EXISTS(SELECT 1 FROM memory_generation_sources gs JOIN memory_source_snapshots ss ON ss.snapshot_id=gs.snapshot_id WHERE gs.conversation_id=c.id AND gs.generation_id=$3 AND gs.status='completed' AND ss.content_status='active' AND ss.source_hash=gs.source_hash) END
 )))`, out.StreamID, out.AcceptedSequence, out.GenerationID).Scan(&out.IndexedSequence)
	if err != nil {
		return out, err
	}
	out.Status = "accepted"
	if out.IndexedSequence >= last {
		out.Status = "complete"
	} else {
		var state string
		var retry sqlpkg.NullTime
		err = r.db.QueryRowContext(ctx, `SELECT j.task_type,j.status,j.attempt,j.failure_code,j.next_retry_at FROM memory_jobs j WHERE j.user_id=$1 AND j.workspace_id=$2 AND (j.generation_id='' OR j.generation_id=$6) AND (j.conversation_id IN(SELECT processing_conversation_id FROM source_events WHERE stream_id=$3 AND sequence_num>$4 AND sequence_num<=$5) OR j.operation_id IN(SELECT operation_id FROM source_events WHERE stream_id=$3 AND sequence_num>$4 AND sequence_num<=$5)) ORDER BY CASE j.status WHEN 'dead_letter' THEN 0 WHEN 'failed' THEN 1 WHEN 'processing' THEN 2 ELSE 3 END,j.updated_at DESC LIMIT 1`, a.UserID, a.WorkspaceID, out.StreamID, out.IndexedSequence, last, out.GenerationID).Scan(&out.Stage, &state, &out.RetryCount, &out.FailureCode, &retry)
		if err != nil && !errors.Is(err, sqlpkg.ErrNoRows) {
			return out, err
		}
		if err == nil {
			out.Status = "processing"
			if state == "failed" || state == "dead_letter" {
				out.Status = "failed"
			}
			out.DeadLetter = state == "dead_letter"
			if retry.Valid {
				out.NextRetryAt = &retry.Time
			}
		}
	}
	return out, nil
}

func (r *PostgresIngestionRepository) RecordJob(ctx context.Context, j domain.MemoryJob) error {
	workspace, err := security.DataScope(ctx, j.UserID, j.WorkspaceID)
	if err != nil {
		return err
	}
	if !security.CanReadPartition(ctx, j.PartitionID) {
		return domain.ErrAccessDenied
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO memory_jobs(id,user_id,workspace_id,partition_id,conversation_id,task_type,status,attempt,failure_code,next_retry_at,queue,operation_id,generation_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(id) DO UPDATE SET generation_id=EXCLUDED.generation_id,queue=EXCLUDED.queue,operation_id=EXCLUDED.operation_id,status=EXCLUDED.status,attempt=EXCLUDED.attempt,failure_code=EXCLUDED.failure_code,next_retry_at=EXCLUDED.next_retry_at,updated_at=now() WHERE memory_jobs.user_id=EXCLUDED.user_id AND memory_jobs.workspace_id=EXCLUDED.workspace_id AND memory_jobs.partition_id=EXCLUDED.partition_id`, j.ID, j.UserID, workspace, j.PartitionID, j.ConversationID, j.TaskType, j.Status, j.Attempt, j.FailureCode, j.NextRetryAt, j.Queue, j.OperationID, j.GenerationID)
	return err
}

func (r *PostgresIngestionRepository) ValidateMemoryTask(ctx context.Context, s domain.MemoryTaskScope) error {
	if s.ReceiptID != "" {
		var id string
		err := r.db.QueryRowContext(ctx, `SELECT id FROM ingestion_receipts WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND partition_id=$4`, s.ReceiptID, s.UserID, s.WorkspaceID, s.PartitionID).Scan(&id)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ErrAccessDenied
		}
		return err
	}

	if s.OperationID != "" {
		var id string
		err := r.db.QueryRowContext(ctx, `SELECT id FROM memory_operations WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND partition_id=$4 AND kind IN('forget','export')`, s.OperationID, s.UserID, s.WorkspaceID, s.PartitionID).Scan(&id)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ErrAccessDenied
		}
		return err
	}

	var workspace string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE user_id=$1 AND id=$2 AND status='active'`, s.UserID, s.WorkspaceID).Scan(&workspace)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.ErrAccessDenied
	}
	if err != nil {
		return err
	}
	if s.PartitionID != "" {
		var id string
		err = r.db.QueryRowContext(ctx, `SELECT p.id FROM memory_partitions p JOIN agents a ON a.user_id=p.user_id AND a.id=p.agent_id WHERE p.user_id=$1 AND p.workspace_id=$2 AND p.id=$3 AND a.status='active'`, s.UserID, s.WorkspaceID, s.PartitionID).Scan(&id)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ErrAccessDenied
		}
		if err != nil {
			return err
		}
	}
	if s.GenerationID != "" {
		var state string
		var active bool
		err = r.db.QueryRowContext(ctx, `SELECT status,is_active FROM memory_generations WHERE user_id=$1 AND workspace_id=$2 AND generation_id=$3`, s.UserID, s.WorkspaceID, s.GenerationID).Scan(&state, &active)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ErrAccessDenied
		}
		if err != nil {
			return err
		}
		if state == "discarded" || state == "activated" && !active {
			return domain.ErrMemoryTaskObsolete
		}
	}
	if s.ConversationID != "" {
		var state string
		var rev int64
		var valid bool
		err = r.db.QueryRowContext(ctx, `SELECT c.finalize_status,c.write_revision,NOT EXISTS(SELECT 1 FROM ingestion_window_sources w JOIN source_messages m ON m.id=w.source_message_id WHERE w.conversation_id=c.id AND (m.revision<>w.source_revision OR m.tombstoned OR m.content_status<>'active')) FROM conversations c WHERE c.user_id=$1 AND c.workspace_id=$2 AND c.partition_id=$3 AND c.id=$4`, s.UserID, s.WorkspaceID, s.PartitionID, s.ConversationID).Scan(&state, &rev, &valid)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ErrAccessDenied
		}
		if err != nil {
			return err
		}
		if !valid || state == "invalidated" || s.ConversationRevision > 0 && s.ConversationRevision != rev {
			return domain.ErrMemoryTaskObsolete
		}
		return nil
	}
	if s.NodeID != "" {
		var state string
		err = r.db.QueryRowContext(ctx, `SELECT access_status FROM memory_nodes WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND id=$4`, s.UserID, s.WorkspaceID, s.PartitionID, s.NodeID).Scan(&state)
		if err == nil && state != "active" {
			return domain.ErrMemoryTaskObsolete
		}
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ErrAccessDenied
		}
		return err
	}
	if s.GenerationID != "" {
		var id string
		err = r.db.QueryRowContext(ctx, `SELECT generation_id FROM memory_generations WHERE user_id=$1 AND workspace_id=$2 AND generation_id=$3`, s.UserID, s.WorkspaceID, s.GenerationID).Scan(&id)
		if errors.Is(err, sqlpkg.ErrNoRows) {
			return domain.ErrAccessDenied
		}
		return err
	}
	return domain.ErrAccessDenied
}
