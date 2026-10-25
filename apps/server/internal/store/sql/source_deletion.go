package sql

import (
	"context"
	sqlpkg "database/sql"
	"time"

	"memstation/apps/server/internal/domain"
)

func lockLifecycleScopeTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace, partition string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, ingestionID("lifecycle-scope", user, workspace, partition))
	return err
}

func sourceErasedTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace, partition, id string, e domain.SourceEvent, namespace string) (bool, error) {
	actors := []string{}
	if e.Speaker.ExternalID != "" {
		actors = append(actors, ingestionID("actor", user, namespace, e.Speaker.ExternalID))
	}
	for _, p := range e.Parts {
		if p.ActorID != "" {
			actors = append(actors, p.ActorID)
		}
		if p.Author != nil && p.Author.ExternalID != "" {
			actors = append(actors, ingestionID("actor", user, namespace, p.Author.ExternalID))
		}
	}
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_tombstones WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND ((target_type='source' AND target_id=$4) OR (cutoff>=$5 AND (target_type='workspace' OR (target_type='actor' AND target_id=ANY($6::text[]))))))`, user, workspace, partition, id, e.OccurredAt, actors).Scan(&blocked)
	return blocked, err
}

func sourceDeletionIntentTx(ctx context.Context, tx *sqlpkg.Tx, user, workspace, partition, id string, sources []string) (domain.DeletionIntent, error) {
	expanded, e := sourceDescendantsTx(ctx, tx, user, workspace, sources)
	if e != nil {
		return domain.DeletionIntent{}, e
	}
	sources = expanded
	in := domain.DeletionIntent{OperationID: id, UserID: user, WorkspaceID: workspace, PartitionID: partition, SourceIDs: sources, Cutoff: time.Now().UTC(), Selector: domain.ForgetSelector{PartitionID: partition}}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT conversation_id FROM ingestion_window_sources WHERE user_id=$1 AND workspace_id=$2 AND source_message_id=ANY($3::text[]) ORDER BY conversation_id`, user, workspace, sources)
	if err != nil {
		return in, err
	}
	in.ConversationIDs, err = readIDs(rows)
	if err != nil {
		return in, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id FROM memory_nodes WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND (source_conversation_id=ANY($4::text[]) OR id IN(SELECT node_id FROM memory_source_dependencies WHERE source_message_id=ANY($5::text[]))) ORDER BY id FOR UPDATE`, user, workspace, partition, in.ConversationIDs, sources)
	if err != nil {
		return in, err
	}
	in.NodeIDs, err = readIDs(rows)
	if err != nil {
		return in, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT blob_id FROM blob_objects WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND blob_id IN(SELECT blob_id FROM message_parts WHERE user_id=$1 AND workspace_id=$2 AND conversation_id=ANY($4::text[])) ORDER BY blob_id`, user, workspace, partition, in.ConversationIDs)
	if err != nil {
		return in, err
	}
	in.BlobIDs, err = readIDs(rows)
	return in, err
}
