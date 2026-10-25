package sql

import (
	"context"
	sqlpkg "database/sql"
	"encoding/json"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func (r *MemoryLifecycleRepository) validateExport(ctx context.Context, op *domain.MemoryOperation) error {
	if op.Kind != "export" || op.Status != "complete" {
		return nil
	}
	var result struct {
		Records []domain.MemoryDocument `json:"records"`
	}
	raw, err := json.Marshal(op.Result)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return err
	}
	user := security.UserIDFromContext(ctx)
	if a, ok := security.AccessFromContext(ctx); ok {
		user = a.UserID
	}
	db, err := r.database()
	if err != nil {
		return err
	}
	repo := NewPostgresMemoryRepository(db)
	for start := 0; start < len(result.Records); start += 200 {
		batch := result.Records[start:min(start+200, len(result.Records))]
		ids := make([]string, 0, len(batch))
		for _, doc := range batch {
			ids = append(ids, doc.NodeID)
		}
		live, err := repo.ListDocumentsByNodeIDs(ctx, user, ids)
		if err != nil {
			return err
		}
		current := map[string]domain.MemoryDocument{}
		for _, doc := range live {
			current[doc.NodeID] = doc
		}
		for _, doc := range batch {
			now, ok := current[doc.NodeID]
			if !ok || now.Revision != doc.Revision || now.Content != doc.Content {
				op.Status = "invalidated"
				op.Result = nil
				_, err = db.ExecContext(ctx, `UPDATE memory_operations SET result='{}',status='invalidated',updated_at=now() WHERE id=$1 AND kind='export'`, op.ID)
				return err
			}
		}
	}
	// The export permission does not imply permission to read raw source text.
	if security.RequireCapability(ctx, domain.CapabilitySourcesRead) != nil {
		docs := security.MemoryContents(ctx, result.Records)
		op.Result = map[string]any{"contract_version": "1.0", "workspace_id": op.WorkspaceID, "partition_id": op.PartitionID, "records": docs, "record_count": len(docs)}
	}
	return nil
}

// ExpireExports removes retained export bodies even when nobody polls the
// operation. The operation identifier remains available for audit/progress.
func (r *MemoryLifecycleRepository) ExpireExports(ctx context.Context) error {
	db, err := r.database()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE memory_operations SET result='{}'::jsonb,status='expired',revision=revision+1,updated_at=now() WHERE kind='export' AND expires_at<=now() AND (status<>'expired' OR result<>'{}'::jsonb)`)
	return err
}

func (r *MemoryLifecycleRepository) Export(ctx context.Context, key string) (domain.MemoryOperation, error) {
	a, err := lifecycleAccess(ctx)
	if err != nil {
		return domain.MemoryOperation{}, err
	}
	db, err := r.database()
	if err != nil {
		return domain.MemoryOperation{}, err
	}
	partition := security.WritePartition(ctx)
	if !security.CanReadPartition(ctx, partition) {
		return domain.MemoryOperation{}, domain.ErrAccessDenied
	}
	id := ingestionID("export", a.UserID, a.WorkspaceID, partition, key)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MemoryOperation{}, err
	}
	defer rollback(tx)
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_operations(id,user_id,workspace_id,partition_id,agent_id,kind,selector,expires_at) VALUES($1,$2,$3,$4,$5,'export','{}',now()+interval '1 day') ON CONFLICT DO NOTHING`, id, a.UserID, a.WorkspaceID, partition, a.AgentID)
	if err != nil {
		return domain.MemoryOperation{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO outbox_events(id,user_id,event_type,aggregate_type,aggregate_id,payload,status,available_at) VALUES($1,$2,'memory.lifecycle','memory_operation',$3,$4,'pending',now()) ON CONFLICT DO NOTHING`, "memory.lifecycle:"+id, a.UserID, id, marshalJSON(map[string]any{"schema_version": 2, "user_id": a.UserID, "workspace_id": a.WorkspaceID, "partition_id": partition, "operation_id": id}))
	if err != nil {
		return domain.MemoryOperation{}, err
	}
	if err = lifecycleAuditTx(ctx, tx, a, "export_memory", "memory_operation", id); err != nil {
		return domain.MemoryOperation{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.MemoryOperation{}, err
	}
	return r.Operation(ctx, id)
}

func (r *MemoryLifecycleRepository) processExport(ctx context.Context, id, user, workspace, partition string) error {
	db, err := r.database()
	if err != nil {
		return err
	}
	bound, err := security.WithDataScope(ctx, user, workspace, partition)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(bound, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var status string
	if err = tx.QueryRowContext(bound, `SELECT status FROM memory_operations WHERE id=$1 AND expires_at>now() FOR UPDATE`, id).Scan(&status); errors.Is(err, sqlpkg.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if status == "invalidated" || status == "complete" {
		return nil
	}
	// Lock the authoritative nodes until the export commits. Erase invalidates the
	// operation result in the same transaction as its source tombstones.
	rows, err := tx.QueryContext(bound, `SELECT id FROM memory_nodes WHERE user_id=$1 AND workspace_id=$2 AND partition_id=$3 AND access_status='active' ORDER BY id FOR SHARE`, user, workspace, partition)
	if err != nil {
		return err
	}
	ids, err := readIDs(rows)
	if err != nil {
		return err
	}
	documents := []domain.MemoryDocument{}
	repo := NewPostgresMemoryRepository(db)
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		docs, err := repo.ListDocumentsByNodeIDs(bound, user, ids[start:end])
		if err != nil {
			return err
		}
		documents = append(documents, docs...)
	}
	_, err = tx.ExecContext(bound, `UPDATE memory_operations SET status='complete',result=$2,revision=revision+1,updated_at=now() WHERE id=$1`, id, marshalJSON(map[string]any{"contract_version": "1.0", "workspace_id": workspace, "partition_id": partition, "records": documents, "record_count": len(documents)}))
	if err != nil {
		return err
	}
	return tx.Commit()
}
