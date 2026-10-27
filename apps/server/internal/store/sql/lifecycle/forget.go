package lifecycle

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
	ingestionstore "memstation/apps/server/internal/store/sql/ingestion"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
	scopestore "memstation/apps/server/internal/store/sql/internal/scope"
)

func (r *MemoryLifecycleRepository) Forget(ctx context.Context, selector domain.ForgetSelector, key string) (domain.MemoryOperation, error) {
	var out domain.MemoryOperation
	a, err := scopestore.LifecycleAccess(ctx)
	if err != nil {
		return out, err
	}
	db, err := r.database()
	if err != nil {
		return out, err
	}
	if r.journal == nil {
		return out, errors.New("independent deletion journal unavailable")
	}
	if !security.CanReadPartition(ctx, selector.PartitionID) {
		return out, domain.ErrAccessDenied
	}
	bound, err := security.WithDataScope(ctx, a.UserID, a.WorkspaceID, selector.PartitionID)
	if err != nil {
		return out, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer dbutilstore.Rollback(tx)
	if err = ingestionstore.LockLifecycleScopeTx(bound, tx, a.UserID, a.WorkspaceID, selector.PartitionID); err != nil {
		return out, err
	}
	id := dbutilstore.IngestionID("forget", a.UserID, a.WorkspaceID, selector.PartitionID, key)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id); err != nil {
		return out, err
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT selector FROM memory_operations WHERE id=$1`, id).Scan(&raw)
	if err == nil {
		var intent domain.DeletionIntent
		if err = json.Unmarshal(raw, &intent); err != nil {
			return out, err
		}
		if ingestionstore.IngestionHash(intent.Selector) != ingestionstore.IngestionHash(selector) {
			return out, domain.ErrAccessConflict
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		return r.Operation(ctx, id)
	}
	if !errors.Is(err, sqlpkg.ErrNoRows) {
		return out, err
	}
	intent := domain.DeletionIntent{RequestHash: ingestionstore.IngestionHash(selector), OperationID: id, UserID: a.UserID, WorkspaceID: a.WorkspaceID, PartitionID: selector.PartitionID, Selector: selector, Cutoff: time.Now().UTC()}
	refs := []string{}
	for _, ref := range selector.SourceRefs {
		if err = ingestionstore.ValidateSourceRefTx(bound, tx, a.UserID, ref); err != nil {
			return out, err
		}
		refs = append(refs, ref.MessageID)
	}
	if selector.ActorID != "" {
		if err = scopestore.ValidateIngestActor(bound, tx, a.UserID, a.WorkspaceID, selector.ActorID); err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM source_messages WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND (id=ANY($4::text[]) OR $5::boolean OR ($6<>'' AND (speaker_actor_id=$6 OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(parts,'null'::jsonb),'[]'::jsonb)) p WHERE p->'author'->>'actor_id'=$6 OR p->>'actor_id'=$6)))) ORDER BY id FOR UPDATE`, a.UserID, a.WorkspaceID, selector.PartitionID, refs, selector.Workspace, selector.ActorID)
	if err != nil {
		return out, err
	}
	intent.SourceIDs, err = dbutilstore.ReadIDs(rows)
	if err != nil {
		return out, err
	}
	intent.SourceIDs, err = ingestionstore.SourceDescendantsTx(bound, tx, a.UserID, a.WorkspaceID, intent.SourceIDs)
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT DISTINCT conversation_id FROM ingestion_window_sources WHERE user_id=$1 AND workspace_id=$2 AND source_message_id=ANY($3::text[]) ORDER BY conversation_id`, a.UserID, a.WorkspaceID, intent.SourceIDs)
	if err != nil {
		return out, err
	}
	intent.ConversationIDs, err = dbutilstore.ReadIDs(rows)
	if err != nil {
		return out, err
	}
	if selector.Workspace {
		rows, err = tx.QueryContext(ctx, `SELECT id FROM conversations WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 ORDER BY id`, a.UserID, a.WorkspaceID, selector.PartitionID)
		if err != nil {
			return out, err
		}
		intent.ConversationIDs, err = dbutilstore.ReadIDs(rows)
		if err != nil {
			return out, err
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT blob_id FROM blob_objects WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND ($4::boolean OR blob_id IN(SELECT blob_id FROM message_parts WHERE user_id=$1 AND workspace_id=$2 AND conversation_id=ANY($5::text[]))) ORDER BY blob_id`, a.UserID, a.WorkspaceID, selector.PartitionID, selector.Workspace, intent.ConversationIDs)
	if err != nil {
		return out, err
	}
	intent.BlobIDs, err = dbutilstore.ReadIDs(rows)
	if err != nil {
		return out, err
	}
	// Node selection includes all generations and descendants of erased evidence.
	rows, err = tx.QueryContext(ctx, `SELECT id FROM memory_nodes n WHERE n.user_id=$1 AND n.workspace_id=$2 AND n.partition_id=$3 AND (n.id=ANY($4::text[]) OR $5::boolean OR ($6<>'' AND n.subject_actor_ids ? $6) OR n.source_conversation_id=ANY($7::text[]) OR n.id IN(SELECT node_id FROM memory_source_dependencies WHERE source_message_id=ANY($8::text[]))) ORDER BY n.id FOR UPDATE`, a.UserID, a.WorkspaceID, selector.PartitionID, selector.NodeIDs, selector.Workspace, selector.ActorID, intent.ConversationIDs, intent.SourceIDs)
	if err != nil {
		return out, err
	}
	intent.NodeIDs, err = dbutilstore.ReadIDs(rows)
	if err != nil {
		return out, err
	}
	if len(selector.NodeIDs) > 0 {
		known := map[string]bool{}
		for _, id := range intent.NodeIDs {
			known[id] = true
		}
		for _, id := range selector.NodeIDs {
			if !known[id] {
				return out, domain.ErrAccessNotFound
			}
		}
	}
	// fsync the authoritative intent before committing its database projection. A
	// failed commit is replayed from the journal; it can never undo a requested erase.
	entry, err := r.journal.Append(ctx, intent)
	if err != nil {
		return out, err
	}
	if err = ingestionstore.ApplyDeletionIntentTx(bound, tx, entry, a.AgentID, r.windowBudget); err != nil {
		return out, err
	}
	if err = ingestionstore.RepairInvalidatedWindowsTx(bound, tx, a.UserID, a.WorkspaceID, selector.PartitionID, r.windowBudget); err != nil {
		return out, err
	}
	if err = lifecycleAuditTx(ctx, tx, a, "forget_memory", "memory_operation", id); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return r.Operation(ctx, id)
}

const operationColumns = `id,workspace_id,partition_id,kind,status,revision,read_blocked,online_cleaned,journal_persisted,backup_status,failure_code,result,expires_at,created_at,updated_at`

func scanOperation(row interface{ Scan(...any) error }) (domain.MemoryOperation, error) {
	var o domain.MemoryOperation
	var result []byte
	var expiry sqlpkg.NullTime
	err := row.Scan(&o.ID, &o.WorkspaceID, &o.PartitionID, &o.Kind, &o.Status, &o.Revision, &o.ReadBlocked, &o.OnlineCleaned, &o.JournalPersisted, &o.BackupStatus, &o.FailureCode, &result, &expiry, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return o, domain.ErrAccessNotFound
	}
	if err != nil {
		return o, err
	}
	if expiry.Valid {
		o.ExpiresAt = &expiry.Time
	}
	if err = json.Unmarshal(result, &o.Result); err != nil {
		return o, err
	}
	return o, nil
}

func (r *MemoryLifecycleRepository) Operation(ctx context.Context, id string) (domain.MemoryOperation, error) {
	a, err := scopestore.LifecycleAccess(ctx)
	if err != nil {
		return domain.MemoryOperation{}, err
	}
	db, err := r.database()
	if err != nil {
		return domain.MemoryOperation{}, err
	}
	op, err := scanOperation(db.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM memory_operations WHERE user_id=$1 AND workspace_id=$2 AND id=$3 AND `+storeaccess.PartitionSQL(ctx, "partition_id"), a.UserID, a.WorkspaceID, id))
	if err != nil {
		return op, err
	}
	if op.ExpiresAt != nil && !op.ExpiresAt.After(time.Now()) {
		op.Result = nil
		op.Status = "expired"
	}
	if err = r.validateExport(ctx, &op); err != nil {
		return domain.MemoryOperation{}, err
	}
	return op, nil
}
