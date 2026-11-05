package eval

import (
	"context"
	"database/sql"
	"fmt"
)

// Task/checkpoint completion does not prove that the VectorStore can see the
// original sources. Enforce the same node/model/version/scope eligibility and
// content identity before spending tokens on answers from an incomplete corpus.
func requireQueryableSourceIndex(ctx context.Context, db *sql.DB, user, conversation, generation, model string, version int) error {
	var total, unavailable int
	err := db.QueryRowContext(ctx, `
 SELECT COUNT(*),COUNT(*) FILTER (WHERE n.id IS NULL OR n.embedding_status<>'ready' OR n.access_status<>'active'
     OR e.node_id IS NULL OR e.content_hash<>encode(sha256(convert_to(s.content,'UTF8')),'hex'))
 FROM conversation_segments s
 LEFT JOIN memory_nodes n ON n.entity_id=s.id AND n.user_id=s.user_id
     AND n.generation_id=s.generation_id AND n.node_type='segment'
 LEFT JOIN memory_embeddings e ON e.node_id=n.id AND e.user_id=n.user_id
     AND e.workspace_id=n.workspace_id AND e.generation_id=n.generation_id
     AND e.node_type=n.node_type AND e.embedding_model=$4 AND e.embedding_version=$5
 WHERE s.user_id=$1 AND s.conversation_id=$2 AND s.generation_id=$3`, user, conversation, generation, model, version).Scan(&total, &unavailable)
	if err != nil {
		return err
	}
	if total == 0 || unavailable > 0 {
		return fmt.Errorf("source index not queryable: %d of %d segments unavailable", unavailable, total)
	}
	return nil
}

func (r *pipelineRuntime) requireSourceIndex(ctx context.Context, user, conversation, generationID string) error {
	generation, err := r.services.Generations.ResolveGeneration(ctx, user, generationID)
	if err != nil {
		return err
	}
	return requireQueryableSourceIndex(ctx, r.db, user, conversation, generation.GenerationID, generation.EmbeddingModel, generation.EmbeddingVersion)
}
