package memorygeneration

import (
	"context"
	"database/sql"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

// FullWorkspaceContext is an explicit administrative operation. Background
// tasks still receive exactly one persisted source partition, never this scope.
func (s *Repository) FullWorkspaceContext(ctx context.Context, user string) (context.Context, error) {
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil || !security.AdminFromContext(ctx) {
		return ctx, domain.ErrAccessDenied
	}
	parts := []string{""}
	if db := s.currentDB(); db != nil {
		rows, err := db.QueryContext(ctx, `SELECT id FROM memory_partitions WHERE user_id=$1 AND workspace_id=$2 ORDER BY id`, user, workspace)
		if err != nil {
			return ctx, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return ctx, err
			}
			parts = append(parts, id)
		}
		if err = rows.Err(); err != nil {
			return ctx, err
		}
	}
	return security.BindAdministrativeWorkspace(ctx, user, workspace, parts...)
}

func enqueueRebuildSourceTx(ctx context.Context, tx *sql.Tx, source domain.MemoryGenerationSource) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO outbox_events(id,user_id,event_type,aggregate_type,aggregate_id,payload,status,available_at)
 SELECT 'memory.rebuild.conversation:'||gs.id,gs.user_id,'memory.rebuild.conversation','memory_generation_source',gs.id,
 jsonb_build_object('schema_version',2,'user_id',gs.user_id,'workspace_id',gs.workspace_id,'partition_id',gs.partition_id,'generation_id',gs.generation_id,'snapshot_id',gs.snapshot_id,'conversation_id',gs.conversation_id),'pending',now()
 FROM memory_generation_sources gs WHERE gs.id=$1 ON CONFLICT DO NOTHING`, source.ID)
	return err
}

func enqueueActivationTx(ctx context.Context, tx *sql.Tx, generation domain.MemoryGeneration) error {
	if generation.Status != domain.MemoryGenerationStatusCompleted || !generation.ActivateOnSuccess {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO outbox_events(id,user_id,event_type,aggregate_type,aggregate_id,payload,status,available_at) VALUES($1,$2,'memory.rebuild.activate','memory_generation',$3,$4,'pending',now()) ON CONFLICT DO NOTHING`, "memory.rebuild.activate:"+generation.GenerationID, generation.UserID, generation.GenerationID, marshalJSON(map[string]any{"schema_version": 2, "user_id": generation.UserID, "workspace_id": generation.WorkspaceID, "partition_id": "", "generation_id": generation.GenerationID}))
	return err
}

func validateActivationTx(ctx context.Context, tx *sql.Tx, g domain.MemoryGeneration) error {
	if err := validateCarriesTx(ctx, tx, g); err != nil {
		return err
	}
	// Check all partitions, including those the initiating agent cannot inspect.
	// Only a fully rebuilt workspace can replace the shared active generation.
	var incomplete bool
	err := tx.QueryRowContext(ctx, `WITH latest AS (
 SELECT DISTINCT ON (conversation_id) snapshot_id,conversation_id,source_hash FROM memory_source_snapshots
 WHERE user_id=$1 AND workspace_id=$2 AND content_status='active' ORDER BY conversation_id,snapshot_version DESC)
 SELECT EXISTS(SELECT 1 FROM latest s WHERE NOT EXISTS(SELECT 1 FROM memory_generation_sources gs WHERE gs.generation_id=$3 AND gs.snapshot_id=s.snapshot_id AND gs.source_hash=s.source_hash AND gs.status='completed'))
 OR EXISTS(SELECT 1 FROM memory_generation_sources gs JOIN memory_source_snapshots s ON s.snapshot_id=gs.snapshot_id WHERE gs.generation_id=$3 AND (s.content_status<>'active' OR gs.status<>'completed'))
 OR EXISTS(SELECT 1 FROM memory_nodes n WHERE n.user_id=$1 AND n.workspace_id=$2 AND n.generation_id=$3 AND n.access_status='active' AND n.embedding_status NOT IN('completed','ready'))`, g.UserID, g.WorkspaceID, g.GenerationID).Scan(&incomplete)
	if err != nil {
		return err
	}
	if incomplete {
		return errors.New("generation does not contain the complete current workspace source set")
	}
	if g.Metadata["rebuild_mode"] != "preserve_authoritative_records" {
		// A newly written record without a captured source, including a concurrent
		// human correction, cannot silently disappear during a re-extraction switch.
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM memory_nodes n JOIN memory_generations current ON current.generation_id=n.generation_id WHERE n.user_id=$1 AND n.workspace_id=$2 AND current.is_active AND n.access_status='active' AND n.node_type IN('summary','segment','fact','impression','event','task','procedure','artifact','entity','agent_action') AND (n.source_conversation_id='' OR n.embedding_revision>0 OR NOT EXISTS(SELECT 1 FROM memory_generation_sources gs WHERE gs.generation_id=$3 AND gs.conversation_id=n.source_conversation_id AND gs.status='completed')))
 OR EXISTS(SELECT 1 FROM memory_candidates c WHERE c.user_id=$1 AND c.workspace_id=$2 AND c.status='approved' AND c.updated_at>$4)`, g.UserID, g.WorkspaceID, g.GenerationID, g.CreatedAt).Scan(&incomplete)
		if err != nil {
			return err
		}
		if incomplete {
			return errors.New("workspace records changed during generation rebuild; rebuild current sources")
		}
	}
	return nil
}
