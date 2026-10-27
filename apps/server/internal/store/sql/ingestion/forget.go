package ingestion

import (
	"context"
	sqlpkg "database/sql"
	"errors"
	"sort"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
)

func ApplyDeletionIntentTx(ctx context.Context, tx *sqlpkg.Tx, entry domain.DeletionEntry, agent string, budget int) error {
	in := entry.Intent
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces WHERE user_id=$1 AND id=$2)`, in.UserID, in.WorkspaceID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		_, err := tx.ExecContext(ctx, `INSERT INTO memory_operations(id,user_id,workspace_id,partition_id,agent_id,kind,status,selector,read_blocked,journal_persisted) VALUES($1,$2,$3,$4,$5,'forget','processing',$6,true,true) ON CONFLICT(id) DO UPDATE SET status='processing',online_cleaned=false,read_blocked=true,journal_persisted=true,updated_at=now()`, in.OperationID, in.UserID, in.WorkspaceID, in.PartitionID, agent, dbutilstore.MarshalJSON(in))
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
				_, err = tx.ExecContext(ctx, `INSERT INTO memory_tombstones(id,user_id,workspace_id,partition_id,operation_id,target_type,target_id,cutoff) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(user_id,workspace_id,partition_id,target_type,target_id) DO UPDATE SET cutoff=GREATEST(memory_tombstones.cutoff,EXCLUDED.cutoff)`, dbutilstore.IngestionID("tombstone", in.UserID, in.WorkspaceID, in.PartitionID, kind, id), in.UserID, in.WorkspaceID, in.PartitionID, in.OperationID, kind, id, in.Cutoff)
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
		if e = RepairInvalidatedWindowsTx(bound, tx, in.UserID, in.WorkspaceID, in.PartitionID, budget); e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO outbox_events(id,user_id,event_type,aggregate_type,aggregate_id,payload,status,available_at) VALUES($1,$2,'memory.lifecycle','memory_operation',$3,$4,'pending',now()) ON CONFLICT DO NOTHING`, "memory.lifecycle:"+in.OperationID, in.UserID, in.OperationID, dbutilstore.MarshalJSON(map[string]any{"schema_version": 2, "user_id": in.UserID, "workspace_id": in.WorkspaceID, "partition_id": in.PartitionID, "operation_id": in.OperationID}))
		if err != nil {
			return err
		}
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO deletion_journal_entries(id,sequence_num,operation_id,payload,digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sequence_num) DO UPDATE SET digest=EXCLUDED.digest WHERE deletion_journal_entries.digest=EXCLUDED.digest AND deletion_journal_entries.operation_id=EXCLUDED.operation_id`, in.OperationID, entry.Sequence, in.OperationID, dbutilstore.MarshalJSON(in), entry.Digest)
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
