package lifecycle

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	ingestionstore "memstation/apps/server/internal/store/sql/ingestion"
	dbutilstore "memstation/apps/server/internal/store/sql/internal/dbutil"
)

// EnsureDeletionJournal is a readiness barrier. It verifies independently stored
// intent history before applying old backups or exposing any restored content.
func (r *MemoryLifecycleRepository) EnsureDeletionJournal(ctx context.Context) error {
	return r.ensureDeletionJournal(ctx)
}

func (r *MemoryLifecycleRepository) ensureDeletionJournal(ctx context.Context) error {
	ctx = security.MaintenanceContext(ctx)
	db, err := r.database()
	if err != nil {
		return err
	}
	if r.journal == nil {
		return errors.New("deletion journal unavailable")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('memstation:deletion-restore',0))`); err != nil {
		return err
	}
	var expected string
	err = tx.QueryRowContext(ctx, `SELECT deployment_id FROM deletion_journal_state WHERE id='current' FOR UPDATE`).Scan(&expected)
	if err != nil && !errors.Is(err, sqlpkg.ErrNoRows) {
		return err
	}
	if expected == "" {
		var populated bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversations) OR EXISTS(SELECT 1 FROM memory_nodes) OR EXISTS(SELECT 1 FROM source_messages) OR EXISTS(SELECT 1 FROM memory_operations)`).Scan(&populated); err != nil {
			return err
		}
		if populated {
			// A backup may predate the journal-state table. Never interpret missing
			// SQL history as permission to replace the independent deletion log.
			if _, _, err = r.journal.Entries(ctx); err != nil {
				return errors.New("existing data requires the original deletion journal")
			}
		}
	}
	deployment, err := r.journal.Initialize(ctx, expected)
	if err != nil {
		return err
	}
	_, entries, err := r.journal.Entries(ctx)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO deletion_journal_state(id,deployment_id) VALUES('current',$1) ON CONFLICT DO NOTHING`, deployment); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence_num,digest FROM deletion_journal_entries ORDER BY sequence_num`)
	if err != nil {
		return err
	}
	known := map[int64]string{}
	for rows.Next() {
		var seq int64
		var digest string
		if err = rows.Scan(&seq, &digest); err != nil {
			_ = rows.Close()
			return err
		}
		known[seq] = digest
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for seq, digest := range known {
		if seq < 1 || seq > int64(len(entries)) || entries[seq-1].Digest != digest {
			return errors.New("deletion journal is missing committed history")
		}
	}
	replayed := []string{}
	for _, entry := range entries {
		if _, ok := known[entry.Sequence]; ok {
			continue
		}
		if err = ingestionstore.ApplyDeletionIntentTx(ctx, tx, entry, "journal-recovery", r.windowBudget); err != nil {
			return err
		}
		replayed = append(replayed, entry.Intent.OperationID)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, `SELECT id FROM memory_operations WHERE kind='forget' AND NOT online_cleaned ORDER BY created_at`)
	if err != nil {
		return err
	}
	pending, err := dbutilstore.ReadIDs(rows)
	if err != nil {
		return err
	}
	// Cleanup occurs before readiness; a filesystem/cache failure leaves the
	// instance unavailable even though SQL reads are already blocked.
	for _, id := range pending {
		if err = r.ProcessOperation(ctx, id); err != nil && !errors.Is(err, domain.ErrAccessNotFound) {
			return err
		}
	}
	if len(replayed) > 0 && r.clearCache != nil {
		return r.clearCache(ctx)
	}
	return nil
}

func (r *MemoryLifecycleRepository) ProcessOperation(ctx context.Context, id string) (processErr error) {
	defer func() {
		if processErr != nil {
			if db, err := r.database(); err == nil {
				_, _ = db.ExecContext(context.WithoutCancel(ctx), `UPDATE memory_operations SET status='failed',failure_code='cleanup_failed',updated_at=now() WHERE id=$1 AND status<>'complete'`, id)
			}
		}
	}()

	db, err := r.database()
	if err != nil {
		return err
	}
	var user, workspace, partition, kind, status string
	var raw []byte
	err = db.QueryRowContext(ctx, `SELECT user_id,workspace_id,partition_id,kind,status,selector FROM memory_operations WHERE id=$1`, id).Scan(&user, &workspace, &partition, &kind, &status, &raw)
	if errors.Is(err, sqlpkg.ErrNoRows) {
		return domain.ErrAccessNotFound
	}
	if err != nil {
		return err
	}
	if status == "complete" || status == "invalidated" {
		return nil
	}
	if kind == "export" {
		return r.processExport(ctx, id, user, workspace, partition)
	}
	var intent domain.DeletionIntent
	if err = json.Unmarshal(raw, &intent); err != nil {
		return err
	}
	if intent.UserID != user || intent.WorkspaceID != workspace || intent.PartitionID != partition {
		return domain.ErrAccessDenied
	}
	bound, err := security.WithDataScope(ctx, user, workspace, partition)
	if err != nil {
		return err
	}
	// Blob deletion is retryable and occurs while source/attachment reads are blocked.
	if len(intent.BlobIDs) > 0 {
		if r.blobs == nil {
			return errors.New("blob erasure storage unavailable")
		}
		for _, blobID := range intent.BlobIDs {
			var blob domain.BlobObject
			blob.UserID = user
			blob.WorkspaceID = workspace
			blob.PartitionID = partition
			blob.BlobID = blobID
			err = db.QueryRowContext(bound, `SELECT storage_key,storage_backend FROM blob_objects WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND blob_id=$4`, user, workspace, partition, blobID).Scan(&blob.StorageKey, &blob.StorageBackend)
			if errors.Is(err, sqlpkg.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err = r.blobs.Delete(bound, blob); err != nil {
				return err
			}
		}
	}
	tx, err := db.BeginTx(bound, nil)
	if err != nil {
		return err
	}
	defer dbutilstore.Rollback(tx)
	// Scope includes revoked publication copies through authoritative source links.
	nodeSet := `SELECT n.id FROM memory_nodes n WHERE n.user_id=$1 AND n.access_status='erased' AND ((n.workspace_id=$2 AND (n.id=ANY($3::text[]) OR n.source_conversation_id=ANY($4::text[]) OR n.id IN(SELECT node_id FROM memory_source_dependencies WHERE source_message_id=ANY($5::text[])))) OR n.id IN(SELECT p.target_node_id FROM memory_publications p WHERE p.source_node_id=ANY($3::text[])))`
	args := []any{user, workspace, intent.NodeIDs, intent.ConversationIDs, intent.SourceIDs}
	statements := []string{
		`DELETE FROM memory_embeddings WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`DELETE FROM entity_aliases WHERE user_id=$1 AND (target_node_id IN (` + nodeSet + `) OR source_node_id IN (` + nodeSet + `))`,
		`DELETE FROM memory_keywords WHERE user_id=$1 AND summary_id IN(SELECT entity_id FROM memory_nodes WHERE id IN (` + nodeSet + `))`,
		`DELETE FROM memory_evidences WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`DELETE FROM memory_relations WHERE user_id=$1 AND (src_node_id IN (` + nodeSet + `) OR dst_node_id IN (` + nodeSet + `))`,
		`UPDATE memory_summaries SET summary='',metadata='{}',topic_label='' WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`UPDATE impressions SET description='',metadata='{}' WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`UPDATE facts SET content='',metadata='{}',subject='' WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`DELETE FROM memory_fact_history WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`UPDATE memory_items SET content='',title='',metadata='{}',deleted_at=now() WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
		`UPDATE key_memories SET value='',deleted_at=now() WHERE user_id=$1 AND node_id IN (` + nodeSet + `)`,
	}
	for _, q := range statements {
		if _, err = tx.ExecContext(bound, q, args...); err != nil {
			return err
		}
	}
	statements = []string{
		`UPDATE messages SET text_content='',content_blocks='[]',tool_calls='[]',tool_result='{}',provider_payload='{}' WHERE user_id=$1 AND workspace_id=$2 AND conversation_id=ANY($3::text[])`,
		`UPDATE conversations SET metadata='{}' WHERE user_id=$1 AND workspace_id=$2 AND id=ANY($3::text[])`,
		`UPDATE conversation_segments SET content='',metadata='{}' WHERE user_id=$1 AND conversation_id=ANY($3::text[]) AND $2<>''`,
		`UPDATE message_parts SET text_content='',raw_payload='',metadata='{}' WHERE user_id=$1 AND workspace_id=$2 AND conversation_id=ANY($3::text[])`,
		`UPDATE message_part_artifacts SET text_content='',metadata='{}' WHERE user_id=$1 AND workspace_id=$2 AND conversation_id=ANY($3::text[])`,
		`UPDATE memory_source_snapshots SET metadata='{}',message_parts='[]',part_artifacts='[]' WHERE user_id=$1 AND workspace_id=$2 AND conversation_id=ANY($3::text[])`,
		`UPDATE memory_source_messages SET text_content='',content_blocks='[]',tool_calls='[]',tool_result='{}',provider_payload='{}' WHERE user_id=$1 AND workspace_id=$2 AND conversation_id=ANY($3::text[])`,
	}
	for _, q := range statements {
		if _, err = tx.ExecContext(bound, q, user, workspace, intent.ConversationIDs); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(bound, `UPDATE blob_objects SET metadata='{}',original_filename='',sha256='' WHERE user_id=$1 AND workspace_id=$2 AND blob_id=ANY($3::text[])`, user, workspace, intent.BlobIDs); err != nil {
		return err
	}
	if intent.Selector.ActorID != "" || intent.Selector.Workspace {
		if err = eraseUnusedActorProfilesTx(bound, tx, intent); err != nil {
			return err
		}
	}
	if r.clearCache != nil {
		if err = r.clearCache(bound); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(bound, `UPDATE memory_operations SET status='complete',online_cleaned=true,read_blocked=true,journal_persisted=true,revision=revision+1,failure_code='',updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Membership is workspace-wide, while erasure may concern one private
// partition. Keep names used by surviving contributions in other partitions;
// delete namespace identifiers only when no workspace still uses the actor.
func eraseUnusedActorProfilesTx(ctx context.Context, tx *sqlpkg.Tx, intent domain.DeletionIntent) error {
	_, err := tx.ExecContext(ctx, `UPDATE workspace_members m SET display_name='',revision=revision+1,updated_at=now()
 WHERE m.user_id=$1 AND m.workspace_id=$2 AND ($3='' OR m.actor_id=$3)
 AND NOT EXISTS(SELECT 1 FROM source_messages s WHERE s.user_id=m.user_id AND s.workspace_id=m.workspace_id AND NOT s.tombstoned AND s.content_status='active' AND (s.speaker_actor_id=m.actor_id OR EXISTS(SELECT 1 FROM jsonb_array_elements(s.parts) p WHERE p->'author'->>'actor_id'=m.actor_id OR p->>'actor_id'=m.actor_id)))
 AND NOT EXISTS(SELECT 1 FROM memory_nodes n WHERE n.user_id=m.user_id AND n.workspace_id=m.workspace_id AND n.access_status='active' AND n.subject_actor_ids ? m.actor_id)
 AND NOT EXISTS(SELECT 1 FROM memory_candidates c WHERE c.user_id=m.user_id AND c.workspace_id=m.workspace_id AND c.status IN('pending','approved') AND c.subject_actor_ids ? m.actor_id)`, intent.UserID, intent.WorkspaceID, intent.Selector.ActorID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM actor_identities i WHERE i.user_id=$1 AND EXISTS(SELECT 1 FROM workspace_members m WHERE m.user_id=i.user_id AND m.workspace_id=$2 AND m.actor_id=i.actor_id AND m.display_name='') AND ($3='' OR i.actor_id=$3)
 AND NOT EXISTS(SELECT 1 FROM workspace_members m WHERE m.user_id=i.user_id AND m.actor_id=i.actor_id AND m.workspace_id<>$2)
 AND NOT EXISTS(SELECT 1 FROM source_messages s WHERE s.user_id=i.user_id AND s.content_status='active' AND NOT s.tombstoned AND (s.speaker_actor_id=i.actor_id OR EXISTS(SELECT 1 FROM jsonb_array_elements(s.parts) p WHERE p->'author'->>'actor_id'=i.actor_id OR p->>'actor_id'=i.actor_id)))`, intent.UserID, intent.WorkspaceID, intent.Selector.ActorID)
	return err
}
