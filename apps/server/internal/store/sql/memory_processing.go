package sql

import (
	"context"
	"encoding/json"
	"errors"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	memorygeneration "memstation/apps/server/internal/store/sql/memorygeneration"
)

func sameProcessingCheckpoint(previous, candidate any) bool {
	type identity struct {
		Version    int    `json:"version"`
		SourceHash string `json:"source_hash"`
	}
	before, _ := json.Marshal(previous)
	after, _ := json.Marshal(candidate)
	var a, b identity
	return json.Unmarshal(before, &a) == nil && json.Unmarshal(after, &b) == nil && a.Version > 0 && a.SourceHash != "" && a == b
}

func validCheckpointKey(key string) bool {
	return key == "_llm_analysis_checkpoint" || key == "_llm_keymemory_checkpoint"
}

func (r *HybridMemoryRepository) StoreSummaryCheckpoint(ctx context.Context, user, generation, id, content, key string, value any) error {
	if db := currentDB(r.db); db != nil {
		return NewPostgresMemoryRepository(db).StoreSummaryCheckpoint(ctx, user, generation, id, content, key, value)
	}
	return r.fallback.StoreSummaryCheckpoint(ctx, user, generation, id, content, key, value)
}

func (r *PostgresMemoryRepository) StoreSummaryCheckpoint(ctx context.Context, user, generation, id, content, key string, value any) error {
	scope, err := security.DataScope(ctx, user, "")
	if err != nil {
		return err
	}
	if !validCheckpointKey(key) {
		return errors.New("invalid summary checkpoint key")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	writable, err := memorygeneration.LockWritableGeneration(ctx, tx, user, scope, generation)
	if err != nil {
		return err
	}
	if !writable {
		return domain.ErrAccessConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE memory_summaries s SET metadata=COALESCE(s.metadata,'{}'::jsonb)||jsonb_build_object($7::text,
 CASE WHEN s.metadata->$7->>'source_hash'=$1::jsonb->>'source_hash' AND s.metadata->$7->>'version'=$1::jsonb->>'version'
 THEN s.metadata->$7 ELSE $1::jsonb END)
 WHERE s.id=$2 AND s.user_id=$3 AND s.generation_id=$4 AND s.summary=$5
 AND EXISTS(SELECT 1 FROM memory_nodes n WHERE n.user_id=s.user_id AND n.id=s.node_id AND n.workspace_id=$6)`, marshalJSON(value), id, user, generation, content, scope, key)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrAccessConflict
	}
	return tx.Commit()
}

func (r *InMemoryMemoryRepository) StoreSummaryCheckpoint(ctx context.Context, user, generation, id, content, key string, value any) error {
	scope, err := security.DataScope(ctx, user, "")
	if err != nil {
		return err
	}
	if !validCheckpointKey(key) {
		return errors.New("invalid summary checkpoint key")
	}
	// Match SQL persistence: repository state must not retain the caller's maps
	// or slices through a checkpoint struct and change after the write returns.
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var snapshot any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for docKey, doc := range r.documents {
		if doc.ID != id || doc.UserID != user || doc.WorkspaceID != scope || doc.GenerationID != generation || doc.Type != domain.MemoryTypeSummary {
			continue
		}
		if doc.Content != content {
			return domain.ErrAccessConflict
		}
		if sameProcessingCheckpoint(doc.Metadata[key], snapshot) {
			return nil
		}
		doc.Metadata = cloneMetadata(doc.Metadata)
		doc.Metadata[key] = snapshot
		r.documents[docKey] = doc
		return nil
	}
	return domain.ErrAccessNotFound
}
