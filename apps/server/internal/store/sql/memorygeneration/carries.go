package memorygeneration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	storeaccess "memstation/apps/server/internal/store/access"
)

// Curated records and publication copies are authoritative, not regenerable
// model output. If present, a workspace rebuild preserves its current records
// and builds new embeddings before an atomic switch. No body is copied to a task.
func prepareCarriesTx(ctx context.Context, tx *sql.Tx, g *domain.MemoryGeneration, active string) error {
	if g.Scope != "user" {
		return nil
	}
	var preserve bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_nodes n WHERE n.user_id=$1 AND n.workspace_id=$2 AND (n.generation_id=$3 OR n.node_type='key_memory') AND n.access_status='active' AND n.node_type IN('summary','segment','fact','impression','key_memory','episode','event','task','procedure','artifact','entity','agent_action') AND (n.source_conversation_id='' OR n.embedding_revision>0 OR EXISTS(SELECT 1 FROM memory_candidates c WHERE c.target_node_id=n.id AND c.status='approved')))`, g.UserID, g.WorkspaceID, active).Scan(&preserve)
	if err != nil || !preserve {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO memory_generation_carries(id,user_id,workspace_id,partition_id,generation_id,node_id,source_generation_id,source_revision,embedding_revision)
 SELECT $1||':'||n.id,n.user_id,n.workspace_id,n.partition_id,$1,n.id,n.generation_id,n.revision,n.embedding_revision FROM memory_nodes n
 WHERE n.user_id=$2 AND n.workspace_id=$3 AND (n.generation_id=$4 OR n.node_type='key_memory') AND n.access_status='active' AND n.node_type IN('summary','segment','fact','impression','key_memory','episode','event','task','procedure','artifact','entity','agent_action') AND `+storeaccess.PartitionSQL(ctx, "n.partition_id"), g.GenerationID, g.UserID, g.WorkspaceID, active)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	g.SourceCount += int(count)
	g.Metadata["rebuild_mode"] = "preserve_authoritative_records"
	_, err = tx.ExecContext(ctx, `UPDATE memory_generations SET source_count=$2,metadata=$3 WHERE generation_id=$1`, g.GenerationID, g.SourceCount, marshalJSON(g.Metadata))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO outbox_events(id,user_id,event_type,aggregate_type,aggregate_id,payload,status,available_at)
 SELECT 'memory.rebuild.carry:'||c.id,c.user_id,'memory.rebuild.carry','memory_generation_carry',c.id,jsonb_build_object('schema_version',2,'user_id',c.user_id,'workspace_id',c.workspace_id,'partition_id',c.partition_id,'generation_id',c.generation_id,'node_id',c.node_id),'pending',now()
 FROM memory_generation_carries c WHERE c.generation_id=$1`, g.GenerationID)
	return err
}

func (s *Repository) CarryDocument(ctx context.Context, user, generation, node string) (source string, revision int64, err error) {
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil {
		return "", 0, err
	}
	db := s.currentDB()
	if db == nil {
		return "", 0, domain.ErrAccessNotFound
	}
	err = db.QueryRowContext(ctx, `SELECT c.source_generation_id,c.embedding_revision FROM memory_generation_carries c JOIN memory_nodes n ON n.id=c.node_id WHERE c.user_id=$1 AND c.workspace_id=$2 AND c.generation_id=$3 AND c.node_id=$4 AND c.source_revision=n.revision AND c.embedding_revision=n.embedding_revision AND `+storeaccess.NodeSQL(ctx, "n"), user, workspace, generation, node).Scan(&source, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrMemoryTaskObsolete
	}
	return
}

// Called with the target generation and parent node locked. A staged embedding
// may differ from its parent's generation only for this exact persisted revision.
func ValidateCarryEmbeddingTx(ctx context.Context, tx *sql.Tx, user, workspace, generation, node string) (bool, error) {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_generation_carries c JOIN memory_nodes n ON n.id=c.node_id WHERE c.user_id=$1 AND c.workspace_id=$2 AND c.generation_id=$3 AND c.node_id=$4 AND c.source_generation_id=n.generation_id AND c.source_revision=n.revision AND c.embedding_revision=n.embedding_revision AND n.access_status='active')`, user, workspace, generation, node).Scan(&allowed)
	return allowed, err
}

func (s *Repository) CompleteCarry(ctx context.Context, user, generation, node string, processErr error) error {
	db := s.currentDB()
	if db == nil {
		return domain.ErrAccessNotFound
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	g, err := lockGenerationTx(ctx, tx, user, generation)
	if err != nil {
		return err
	}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT status FROM memory_generation_carries WHERE user_id=$1 AND workspace_id=$2 AND generation_id=$3 AND node_id=$4 AND `+storeaccess.PartitionSQL(ctx, "partition_id")+` FOR UPDATE`, user, g.WorkspaceID, generation, node).Scan(&previous)
	if err != nil {
		return err
	}
	state := domain.MemoryGenerationSourceStatusCompleted
	reason := ""
	if processErr != nil {
		state = domain.MemoryGenerationSourceStatusFailed
		reason = "embedding_failed"
	}
	next, _, changed := transition(g, domain.MemoryGenerationSource{Status: domain.MemoryGenerationSourceStatus(previous)}, state, reason, 1)
	if !changed {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE memory_generation_carries SET status=$3,last_error=$4,updated_at=now() WHERE generation_id=$1 AND node_id=$2`, generation, node, state, reason); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE memory_generations SET status=$2,processed_count=$3,embedding_count=$4,failure_count=$5,last_error=$6,updated_at=now() WHERE generation_id=$1`, generation, next.Status, next.ProcessedCount, next.EmbeddingCount, next.FailureCount, next.LastError); err != nil {
		return err
	}
	if err = enqueueActivationTx(ctx, tx, next); err != nil {
		return err
	}
	return tx.Commit()
}

func validateCarriesTx(ctx context.Context, tx *sql.Tx, g domain.MemoryGeneration) error {
	if g.Metadata["rebuild_mode"] != "preserve_authoritative_records" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT n.id FROM memory_nodes n JOIN memory_generation_carries c ON c.node_id=n.id WHERE c.generation_id=$1 ORDER BY n.id FOR UPDATE OF n`, g.GenerationID)
	if err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	var invalid bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_generation_carries c JOIN memory_nodes n ON n.id=c.node_id WHERE c.generation_id=$1 AND (c.status<>'completed' OR c.source_revision<>n.revision OR c.source_generation_id<>n.generation_id OR n.access_status<>'active' OR NOT EXISTS(SELECT 1 FROM memory_embeddings e WHERE e.node_id=n.id AND e.generation_id=$1 AND e.embedding_version=$4)))
 OR EXISTS(SELECT 1 FROM memory_nodes n LEFT JOIN memory_generations g ON g.generation_id=n.generation_id WHERE n.user_id=$2 AND n.workspace_id=$3 AND (g.is_active OR n.node_type='key_memory') AND n.access_status='active' AND n.node_type IN('summary','segment','fact','impression','key_memory','episode','event','task','procedure','artifact','entity','agent_action') AND NOT EXISTS(SELECT 1 FROM memory_generation_carries c WHERE c.generation_id=$1 AND c.node_id=n.id))`, g.GenerationID, g.UserID, g.WorkspaceID, g.EmbeddingVersion).Scan(&invalid)
	if err != nil {
		return err
	}
	if invalid {
		return fmt.Errorf("authoritative records changed during generation rebuild; rebuild the current revision")
	}
	return nil
}

func activateCarriesTx(ctx context.Context, tx *sql.Tx, g domain.MemoryGeneration) error {
	if g.Metadata["rebuild_mode"] != "preserve_authoritative_records" {
		return nil
	}
	// Calendar nodes have no independent body or vector. Their partition remains
	// unchanged; move only the old generations represented in this exact rebuild.
	if _, err := tx.ExecContext(ctx, `UPDATE memory_nodes SET generation_id=$1 WHERE user_id=$2 AND workspace_id=$3 AND access_status='active' AND node_type IN('time_tier','calendar_day','calendar_month','calendar_year') AND generation_id IN(SELECT source_generation_id FROM memory_generation_carries WHERE generation_id=$1)`, g.GenerationID, g.UserID, g.WorkspaceID); err != nil {
		return err
	}
	for _, table := range []string{"memory_nodes", "memory_summaries", "facts", "impressions", "memory_items", "memory_fact_history", "memory_evidences", "conversation_segments"} {
		id := "node_id"
		if table == "conversation_segments" {
			if _, err := tx.ExecContext(ctx, `UPDATE conversation_segments SET generation_id=$1 WHERE id IN(SELECT entity_id FROM memory_nodes WHERE id IN(SELECT node_id FROM memory_generation_carries WHERE generation_id=$1) AND node_type='segment')`, g.GenerationID); err != nil {
				return err
			}
			continue
		}
		if table == "memory_nodes" {
			id = "id"
		}
		exclude := ""
		if table == "memory_nodes" {
			exclude = " AND node_type<>'key_memory'"
		}
		_, err := tx.ExecContext(ctx, `UPDATE `+table+` SET generation_id=$1 WHERE `+id+` IN(SELECT node_id FROM memory_generation_carries WHERE generation_id=$1)`+exclude, g.GenerationID)
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memory_fact_history SET document=jsonb_set(document,'{generation_id}',to_jsonb($1::text)) WHERE generation_id=$1 AND node_id IN(SELECT node_id FROM memory_generation_carries WHERE generation_id=$1)`, g.GenerationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE entity_aliases SET generation_id=$1 WHERE user_id=$2 AND target_node_id IN(SELECT node_id FROM memory_generation_carries WHERE generation_id=$1)`, g.GenerationID, g.UserID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memory_embeddings SET generation_id='' WHERE generation_id=$1 AND node_type='key_memory'`, g.GenerationID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE memory_relations r SET generation_id=$1 WHERE r.user_id=$2 AND EXISTS(SELECT 1 FROM memory_nodes n WHERE n.id=r.src_node_id AND n.workspace_id=$3 AND (n.generation_id=$1 OR n.node_type='key_memory')) AND EXISTS(SELECT 1 FROM memory_nodes n WHERE n.id=r.dst_node_id AND n.workspace_id=$3 AND (n.generation_id=$1 OR n.node_type='key_memory'))`, g.GenerationID, g.UserID, g.WorkspaceID)
	return err
}
