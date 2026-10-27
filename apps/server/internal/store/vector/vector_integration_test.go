package vector

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
)

func TestPGVectorStoreEmbeddingRevisionCAS(t *testing.T) {
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
	const userID = "vector-cas-user"
	if _, err = db.ExecContext(ctx, `INSERT INTO workspaces(id,user_id,name,capabilities) VALUES ('personal-' || encode(sha256(convert_to($1,'UTF8')),'hex'),$1,'Fixture','[]') ON CONFLICT DO NOTHING`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO memory_generations(generation_id,user_id,workspace_id,status) VALUES('gen-cas',$1,$2,'processing') ON CONFLICT DO NOTHING`, userID, domain.PersonalWorkspaceID(userID)); err != nil {
		t.Fatal(err)
	}
	const nodeID = "vector-cas-node"
	if _, err = db.ExecContext(ctx, `DELETE FROM memory_nodes WHERE user_id = $1 AND id = $2`, userID, nodeID); err != nil {
		t.Fatalf("clean node: %v", err)
	}
	if _, err = db.ExecContext(ctx, `
		INSERT INTO memory_nodes (workspace_id, id, generation_id, user_id, node_type, entity_id, embedding_status, embedding_revision)
		VALUES ('personal-' || encode(sha256(convert_to($2,'UTF8')),'hex'),$1, 'gen-cas', $2, 'fact', 'fact-cas', 'pending', 2)
	`, nodeID, userID); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM memory_nodes WHERE user_id = $1 AND id = $2`, userID, nodeID)
	})

	store := NewPGVectorStore(runtimeconfig.NewStaticDBHandle(db))
	upsert := func(revision int64, contentHash string, vector []float32) error {
		return store.Upsert(ctx, domain.VectorUpsertRequest{
			UserID:            userID,
			GenerationID:      "gen-cas",
			NodeID:            nodeID,
			NodeType:          string(domain.MemoryTypeFact),
			EmbeddingVersion:  1,
			EmbeddingModel:    "integration-test",
			ContentHash:       contentHash,
			EmbeddingRevision: revision,
			Vector:            vector,
		})
	}
	if err = upsert(1, "stale", []float32{1, 0, 0}); err != nil {
		t.Fatalf("upsert stale revision: %v", err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_embeddings WHERE user_id = $1 AND node_id = $2`, userID, nodeID).Scan(&count); err != nil {
		t.Fatalf("count stale embedding: %v", err)
	}
	if count != 0 {
		t.Fatalf("stale revision wrote %d embeddings", count)
	}

	if err = upsert(2, "current", []float32{0, 1, 0}); err != nil {
		t.Fatalf("upsert current revision: %v", err)
	}
	var status string
	if err = db.QueryRowContext(ctx, `SELECT embedding_status FROM memory_nodes WHERE user_id = $1 AND id = $2`, userID, nodeID).Scan(&status); err != nil {
		t.Fatalf("read node status: %v", err)
	}
	if status != "ready" {
		t.Fatalf("expected ready node, got %q", status)
	}

	if _, err = db.ExecContext(ctx, `UPDATE memory_nodes SET embedding_status = 'pending', embedding_revision = 3 WHERE user_id = $1 AND id = $2`, userID, nodeID); err != nil {
		t.Fatalf("advance revision: %v", err)
	}
	if err = upsert(2, "late-stale", []float32{0, 0, 1}); err != nil {
		t.Fatalf("upsert late stale revision: %v", err)
	}
	var contentHash string
	if err = db.QueryRowContext(ctx, `SELECT content_hash FROM memory_embeddings WHERE user_id = $1 AND node_id = $2`, userID, nodeID).Scan(&contentHash); err != nil {
		t.Fatalf("read embedding hash: %v", err)
	}
	if contentHash != "current" {
		t.Fatalf("late stale task replaced current embedding with %q", contentHash)
	}
}
