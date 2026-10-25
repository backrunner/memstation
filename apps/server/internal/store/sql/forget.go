package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
)

func (r *MemoryLifecycleRepository) Forget(ctx context.Context, selector domain.ForgetSelector, key string) (domain.MemoryOperation, error) {
	var out domain.MemoryOperation
	a, err := lifecycleAccess(ctx)
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
	defer rollback(tx)
	if err = lockLifecycleScopeTx(bound, tx, a.UserID, a.WorkspaceID, selector.PartitionID); err != nil {
		return out, err
	}
	id := ingestionID("forget", a.UserID, a.WorkspaceID, selector.PartitionID, key)
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
		if ingestionHash(intent.Selector) != ingestionHash(selector) {
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
	intent := domain.DeletionIntent{RequestHash: ingestionHash(selector), OperationID: id, UserID: a.UserID, WorkspaceID: a.WorkspaceID, PartitionID: selector.PartitionID, Selector: selector, Cutoff: time.Now().UTC()}
	refs := []string{}
	for _, ref := range selector.SourceRefs {
		if err = validateSourceRefTx(bound, tx, a.UserID, ref); err != nil {
			return out, err
		}
		refs = append(refs, ref.MessageID)
	}
	if selector.ActorID != "" {
		if err = validateIngestActor(bound, tx, a.UserID, a.WorkspaceID, selector.ActorID); err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM source_messages WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND (id=ANY($4::text[]) OR $5::boolean OR ($6<>'' AND (speaker_actor_id=$6 OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(parts,'null'::jsonb),'[]'::jsonb)) p WHERE p->'author'->>'actor_id'=$6 OR p->>'actor_id'=$6)))) ORDER BY id FOR UPDATE`, a.UserID, a.WorkspaceID, selector.PartitionID, refs, selector.Workspace, selector.ActorID)
	if err != nil {
		return out, err
	}
	intent.SourceIDs, err = readIDs(rows)
	if err != nil {
		return out, err
	}
	intent.SourceIDs, err = sourceDescendantsTx(bound, tx, a.UserID, a.WorkspaceID, intent.SourceIDs)
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT DISTINCT conversation_id FROM ingestion_window_sources WHERE user_id=$1 AND workspace_id=$2 AND source_message_id=ANY($3::text[]) ORDER BY conversation_id`, a.UserID, a.WorkspaceID, intent.SourceIDs)
	if err != nil {
		return out, err
	}
	intent.ConversationIDs, err = readIDs(rows)
	if err != nil {
		return out, err
	}
	if selector.Workspace {
		rows, err = tx.QueryContext(ctx, `SELECT id FROM conversations WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 ORDER BY id`, a.UserID, a.WorkspaceID, selector.PartitionID)
		if err != nil {
			return out, err
		}
		intent.ConversationIDs, err = readIDs(rows)
		if err != nil {
			return out, err
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT blob_id FROM blob_objects WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND ($4::boolean OR blob_id IN(SELECT blob_id FROM message_parts WHERE user_id=$1 AND workspace_id=$2 AND conversation_id=ANY($5::text[]))) ORDER BY blob_id`, a.UserID, a.WorkspaceID, selector.PartitionID, selector.Workspace, intent.ConversationIDs)
	if err != nil {
		return out, err
	}
	intent.BlobIDs, err = readIDs(rows)
	if err != nil {
		return out, err
	}
	// Node selection includes all generations and descendants of erased evidence.
	rows, err = tx.QueryContext(ctx, `SELECT id FROM memory_nodes n WHERE n.user_id=$1 AND n.workspace_id=$2 AND n.partition_id=$3 AND (n.id=ANY($4::text[]) OR $5::boolean OR ($6<>'' AND n.subject_actor_ids ? $6) OR n.source_conversation_id=ANY($7::text[]) OR n.id IN(SELECT node_id FROM memory_source_dependencies WHERE source_message_id=ANY($8::text[]))) ORDER BY n.id FOR UPDATE`, a.UserID, a.WorkspaceID, selector.PartitionID, selector.NodeIDs, selector.Workspace, selector.ActorID, intent.ConversationIDs, intent.SourceIDs)
	if err != nil {
		return out, err
	}
	intent.NodeIDs, err = readIDs(rows)
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
	if err = applyDeletionIntentTx(bound, tx, entry, a.AgentID, r.windowBudget); err != nil {
		return out, err
	}
	if err = repairInvalidatedWindowsTx(bound, tx, a.UserID, a.WorkspaceID, selector.PartitionID, r.windowBudget); err != nil {
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

func readIDs(rows *sqlpkg.Rows) ([]string, error) {
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func applyDeletionIntentTx(ctx context.Context, tx *sqlpkg.Tx, entry domain.DeletionEntry, agent string, budget int) error {
	in := entry.Intent
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces WHERE user_id=$1 AND id=$2)`, in.UserID, in.WorkspaceID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		_, err := tx.ExecContext(ctx, `INSERT INTO memory_operations(id,user_id,workspace_id,partition_id,agent_id,kind,status,selector,read_blocked,journal_persisted) VALUES($1,$2,$3,$4,$5,'forget','processing',$6,true,true) ON CONFLICT(id) DO UPDATE SET status='processing',online_cleaned=false,read_blocked=true,journal_persisted=true,updated_at=now()`, in.OperationID, in.UserID, in.WorkspaceID, in.PartitionID, agent, marshalJSON(in))
		if err != nil {
			return err
		}
		targets := map[string][]string{"source": in.SourceIDs, "node": in.NodeIDs, "conversation": in.ConversationIDs}
		if in.Selector.ActorID != "" {
			targets["actor"] = []string{in.Selector.ActorID}
		}
		if in.Selector.Workspace {
			targets["workspace"] = []string{in.WorkspaceID}
		}
		kinds := []string{}
		for kind := range targets {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			for _, id := range targets[kind] {
				_, err = tx.ExecContext(ctx, `INSERT INTO memory_tombstones(id,user_id,workspace_id,partition_id,operation_id,target_type,target_id,cutoff) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(user_id,workspace_id,partition_id,target_type,target_id) DO UPDATE SET cutoff=GREATEST(memory_tombstones.cutoff,EXCLUDED.cutoff)`, ingestionID("tombstone", in.UserID, in.WorkspaceID, in.PartitionID, kind, id), in.UserID, in.WorkspaceID, in.PartitionID, in.OperationID, kind, id, in.Cutoff)
				if err != nil {
					return err
				}
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE source_messages SET tombstoned=true,content_status='erased',parts='[]',reply_to=NULL,speaker_actor_id='',updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND id=ANY($3::text[])`, in.UserID, in.WorkspaceID, in.SourceIDs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE memory_nodes SET access_status='erased',revision=revision+1,updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND (id=ANY($3::text[]) OR source_conversation_id=ANY($4::text[]) OR id IN(SELECT node_id FROM memory_source_dependencies WHERE source_message_id=ANY($5::text[]))) AND access_status<>'erased'`, in.UserID, in.WorkspaceID, in.NodeIDs, in.ConversationIDs, in.SourceIDs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE memory_nodes n SET access_status='erased',revision=n.revision+1,updated_at=now() WHERE n.user_id=$1 AND n.id IN(SELECT p.target_node_id FROM memory_publications p JOIN memory_nodes s ON s.id=p.source_node_id WHERE p.user_id=$1 AND s.workspace_id=$2 AND s.access_status='erased') AND n.access_status<>'erased'`, in.UserID, in.WorkspaceID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE conversations SET finalize_status='invalidated',summary_status='failed',analysis_status='failed',updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND id=ANY($3::text[])`, in.UserID, in.WorkspaceID, in.ConversationIDs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE memory_source_snapshots SET content_status='erased',updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND conversation_id=ANY($3::text[])`, in.UserID, in.WorkspaceID, in.ConversationIDs)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE source_events SET operation_id=$1,processing_conversation_id='' WHERE user_id=$2 AND workspace_id=$3 AND source_message_id=ANY($4::text[])`, in.OperationID, in.UserID, in.WorkspaceID, in.SourceIDs); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE blob_objects SET deleted_at=COALESCE(deleted_at,now()),updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND blob_id=ANY($3::text[])`, in.UserID, in.WorkspaceID, in.BlobIDs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE memory_candidates SET status='invalidated',content='',source_refs='[]',subject_actor_ids='[]',revision=revision+1,updated_at=now() WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND ($4::boolean OR target_node_id=ANY($5::text[]) OR ($6<>'' AND subject_actor_ids ? $6) OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(source_refs,'null'::jsonb),'[]'::jsonb)) ref WHERE ref->>'message_id'=ANY($7::text[])))`, in.UserID, in.WorkspaceID, in.PartitionID, in.Selector.Workspace, in.NodeIDs, in.Selector.ActorID, in.SourceIDs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE memory_operations o SET status='invalidated',result='{}',updated_at=now() WHERE o.user_id=$1 AND o.kind='export' AND ((o.workspace_id=$2 AND o.partition_id=$3) OR EXISTS(SELECT 1 FROM memory_publications p JOIN memory_nodes n ON n.id=p.source_node_id WHERE p.user_id=o.user_id AND p.target_workspace_id=o.workspace_id AND p.target_partition_id=o.partition_id AND n.workspace_id=$2 AND n.access_status='erased'))`, in.UserID, in.WorkspaceID, in.PartitionID)
		if err != nil {
			return err
		}
		bound, e := security.WithDataScope(ctx, in.UserID, in.WorkspaceID, in.PartitionID)
		if e != nil {
			return e
		}
		if e = repairInvalidatedWindowsTx(bound, tx, in.UserID, in.WorkspaceID, in.PartitionID, budget); e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO outbox_events(id,user_id,event_type,aggregate_type,aggregate_id,payload,status,available_at) VALUES($1,$2,'memory.lifecycle','memory_operation',$3,$4,'pending',now()) ON CONFLICT DO NOTHING`, "memory.lifecycle:"+in.OperationID, in.UserID, in.OperationID, marshalJSON(map[string]any{"schema_version": 2, "user_id": in.UserID, "workspace_id": in.WorkspaceID, "partition_id": in.PartitionID, "operation_id": in.OperationID}))
		if err != nil {
			return err
		}
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO deletion_journal_entries(id,sequence_num,operation_id,payload,digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sequence_num) DO UPDATE SET digest=EXCLUDED.digest WHERE deletion_journal_entries.digest=EXCLUDED.digest AND deletion_journal_entries.operation_id=EXCLUDED.operation_id`, in.OperationID, entry.Sequence, in.OperationID, marshalJSON(in), entry.Digest)
	if err != nil {
		return err
	}

	if n, e := inserted.RowsAffected(); e != nil {
		return e
	} else if n != 1 {
		return errors.New("deletion journal projection conflicts with committed history")
	}
	_, err = tx.ExecContext(ctx, `UPDATE deletion_journal_state SET sequence_num=GREATEST(sequence_num,$1),digest=CASE WHEN sequence_num<=$1 THEN $2 ELSE digest END,updated_at=now() WHERE id='current'`, entry.Sequence, entry.Digest)
	return err
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
	a, err := lifecycleAccess(ctx)
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
