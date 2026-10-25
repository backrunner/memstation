package sql

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	lifecycleservice "memstation/apps/server/internal/service/lifecycle"
	storeaccess "memstation/apps/server/internal/store/access"
)

func (r *MemoryLifecycleRepository) Overview(ctx context.Context, after string, limit int) (domain.MemoryControlOverview, error) {
	out := domain.MemoryControlOverview{Actors: []domain.WorkspaceActor{}, Sources: []domain.SourceSummary{}, Receipts: []domain.IngestionReceipt{}, Jobs: []domain.MemoryJob{}, Operations: []domain.MemoryOperation{}, Limit: limit}
	positions := map[string]string{}
	if after != "" {
		raw, err := base64.RawURLEncoding.DecodeString(after)
		if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &positions) != nil {
			return out, lifecycleservice.ErrInvalidInput
		}
	}
	a, err := lifecycleAccess(ctx)
	if err != nil {
		return out, err
	}
	if !security.AdminFromContext(ctx) {
		return out, domain.ErrAccessDenied
	}
	db, err := r.database()
	if err != nil {
		return out, err
	}
	rows, err := db.QueryContext(ctx, `SELECT m.actor_id,visible.display_name,a.kind,visible.revision FROM workspace_members m JOIN actors a ON a.id=m.actor_id AND a.user_id=m.user_id
 JOIN LATERAL (
  SELECT COALESCE(msg.provider_payload->'speaker'->>'display_name','') AS display_name,s.revision
  FROM source_messages s JOIN ingestion_window_sources w ON w.source_message_id=s.id AND w.source_revision=s.revision AND w.user_id=s.user_id AND w.workspace_id=s.workspace_id JOIN messages msg ON msg.user_id=w.user_id AND msg.workspace_id=w.workspace_id AND msg.conversation_id=w.conversation_id AND msg.sequence_num=w.sequence_num
  WHERE s.user_id=m.user_id AND s.workspace_id=m.workspace_id AND s.speaker_actor_id=m.actor_id AND NOT s.tombstoned AND s.content_status='active' AND `+storeaccess.PartitionSQL(ctx, "s.partition_id")+`
  ORDER BY s.occurred_at DESC,msg.created_at DESC LIMIT 1
 ) visible ON true
 WHERE m.user_id=$1 AND m.workspace_id=$2 AND m.actor_id>$3 AND m.status='active' ORDER BY m.actor_id LIMIT $4`, a.UserID, a.WorkspaceID, positions["actors"], limit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.WorkspaceActor
		if err = rows.Scan(&v.ActorID, &v.DisplayName, &v.Kind, &v.Revision); err != nil {
			break
		}
		out.Actors = append(out.Actors, v)
	}
	scanErr := rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, err
	}
	if scanErr != nil {
		return out, scanErr
	}
	rows, err = db.QueryContext(ctx, `SELECT id,revision,CASE WHEN tombstoned THEN '' ELSE speaker_actor_id END,content_status FROM source_messages WHERE user_id=$1 AND workspace_id=$2 AND id>$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` ORDER BY id LIMIT $4`, a.UserID, a.WorkspaceID, positions["sources"], limit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.SourceSummary
		if err = rows.Scan(&v.ID, &v.Revision, &v.SpeakerActorID, &v.Status); err != nil {
			break
		}
		out.Sources = append(out.Sources, v)
	}
	scanErr = rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, err
	}
	if scanErr != nil {
		return out, scanErr
	}
	for _, kind := range []string{"ingestion_receipts", "memory_operations"} {
		rows, err = db.QueryContext(ctx, `SELECT id FROM `+kind+` WHERE user_id=$1 AND workspace_id=$2 AND id>$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` ORDER BY id LIMIT $4`, a.UserID, a.WorkspaceID, positions[kind], limit)
		if err != nil {
			return out, err
		}
		ids, e := readIDs(rows)
		if e != nil {
			return out, e
		}
		for _, id := range ids {
			if kind == "ingestion_receipts" {
				v, e := NewPostgresIngestionRepository(db).Progress(ctx, id)
				if e != nil {
					return out, e
				}
				out.Receipts = append(out.Receipts, v)
			} else {
				v, e := r.Operation(ctx, id)
				if e != nil {
					return out, e
				}
				v.Result = nil
				out.Operations = append(out.Operations, v)
			}
		}
	}
	rows, err = db.QueryContext(ctx, `SELECT id,queue,generation_id,operation_id,user_id,workspace_id,partition_id,conversation_id,task_type,status,attempt,failure_code,next_retry_at FROM memory_jobs WHERE user_id=$1 AND workspace_id=$2 AND id>$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` ORDER BY id LIMIT $4`, a.UserID, a.WorkspaceID, positions["jobs"], limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v domain.MemoryJob
		if err = rows.Scan(&v.ID, &v.Queue, &v.GenerationID, &v.OperationID, &v.UserID, &v.WorkspaceID, &v.PartitionID, &v.ConversationID, &v.TaskType, &v.Status, &v.Attempt, &v.FailureCode, &v.NextRetryAt); err != nil {
			return out, err
		}
		out.Jobs = append(out.Jobs, v)
	}
	more := false
	advance := func(kind string, count int, last string) {
		if count > 0 {
			positions[kind] = last
		}
		if count == limit {
			more = true
		}
	}
	if n := len(out.Actors); n > 0 {
		advance("actors", n, out.Actors[n-1].ActorID)
	}
	if n := len(out.Sources); n > 0 {
		advance("sources", n, out.Sources[n-1].ID)
	}
	if n := len(out.Receipts); n > 0 {
		advance("ingestion_receipts", n, out.Receipts[n-1].ID)
	}
	if n := len(out.Operations); n > 0 {
		advance("memory_operations", n, out.Operations[n-1].ID)
	}
	if n := len(out.Jobs); n > 0 {
		advance("jobs", n, out.Jobs[n-1].ID)
	}
	if more {
		raw, _ := json.Marshal(positions)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, rows.Err()
}

func (r *MemoryLifecycleRepository) ReplayJob(ctx context.Context, id string, run func(string, string) error) error {
	if !security.AdminFromContext(ctx) {
		return domain.ErrAccessDenied
	}
	a, err := lifecycleAccess(ctx)
	if err != nil {
		return err
	}
	db, err := r.database()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var queue, state string
	err = tx.QueryRowContext(ctx, `SELECT queue,status FROM memory_jobs WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` FOR UPDATE`, id, a.UserID, a.WorkspaceID).Scan(&queue, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrAccessNotFound
	}
	if err != nil {
		return err
	}
	if state != "failed" && state != "dead_letter" {
		return domain.ErrAccessConflict
	}
	// Run the original task ID: a separate replacement would leave the failed
	// attempt blocking the continuous indexed watermark indefinitely.
	if err = run(queue, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE memory_jobs SET status='queued',next_retry_at=NULL,updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if err = lifecycleAuditTx(ctx, tx, a, "replay_memory_job", "memory_job", id); err != nil {
		return err
	}
	return tx.Commit()
}
