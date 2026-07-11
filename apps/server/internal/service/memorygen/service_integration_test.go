package memorygen

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
)

func TestDiscardGenerationDeletesInactiveGraphData(t *testing.T) {
	databaseURL := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const userID = "generation-discard-user"
	const generationID = "generation-discard-candidate"
	const nodeID = "generation-discard-node"
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM memory_nodes WHERE user_id = $1`, userID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM memory_generations WHERE user_id = $1`, userID)
	}
	cleanup()
	t.Cleanup(cleanup)
	now := time.Now().UTC()
	if _, err = db.ExecContext(ctx, `
		INSERT INTO memory_generations (
			generation_id, user_id, status, scope, is_active, structure_version, embedding_version,
			embedding_model, activate_on_success, source_count, processed_count, embedding_count,
			failure_count, metadata, created_at, updated_at
		) VALUES ($1,$2,'completed','conversation',FALSE,1,2,'integration-test',FALSE,1,1,1,0,'{}',$3,$3)
	`, generationID, userID, now); err != nil {
		t.Fatalf("insert generation: %v", err)
	}
	if _, err = db.ExecContext(ctx, `
		INSERT INTO memory_nodes (id, generation_id, user_id, node_type, entity_id, embedding_status)
		VALUES ($1,$2,$3,'fact','fact-discard','ready')
	`, nodeID, generationID, userID); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if _, err = db.ExecContext(ctx, `
		INSERT INTO facts (id, generation_id, user_id, conversation_id, node_id, content)
		VALUES ('fact-discard',$1,$2,'conv-discard',$3,'discard me')
	`, generationID, userID, nodeID); err != nil {
		t.Fatalf("insert fact: %v", err)
	}
	if _, err = db.ExecContext(ctx, `
		INSERT INTO memory_embeddings (embedding_id, generation_id, user_id, node_id, node_type, embedding_version, embedding_model, content_hash, vector)
		VALUES ('embedding-discard',$1,$2,$3,'fact',2,'integration-test','hash','[1,0,0]')
	`, generationID, userID, nodeID); err != nil {
		t.Fatalf("insert embedding: %v", err)
	}

	service := NewService(runtimeconfig.NewStaticDBHandle(db), "integration-test")
	discarded, err := service.DiscardGeneration(ctx, userID, generationID)
	if err != nil {
		t.Fatalf("discard generation: %v", err)
	}
	if discarded.Status != domain.MemoryGenerationStatusDiscarded {
		t.Fatalf("expected discarded status, got %s", discarded.Status)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_nodes WHERE user_id = $1 AND generation_id = $2`, userID, generationID).Scan(&count); err != nil {
		t.Fatalf("count remaining nodes: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected graph data to be deleted, found %d nodes", count)
	}
}
