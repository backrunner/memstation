package memory

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	vectorstore "memstation/apps/server/internal/store/vector"

	"github.com/google/uuid"
)

// Uses the actual SQL status gate. The in-memory vector store does not join
// memory_nodes, so a fake-vector worker test cannot detect this lifecycle bug.
func TestPostgresSegmentRewritePreservesQueryableEmbedding(t *testing.T) {
	dsn := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, variant := range []string{"unchanged", "changed_content", "missing_vector", "pending", "different_generation", "different_model", "different_version", "unverified_hash"} {
		t.Run(variant, func(t *testing.T) {
			owner := "source-ready-" + uuid.NewString()
			repo := NewPostgresMemoryRepository(db)
			provisionFixtureGeneration(t, repo, ctx, owner, "")
			doc := domain.MemoryDocument{ID: uuid.NewString(), UserID: owner, Type: domain.MemoryTypeSegment, Content: "Mira visits the café to choose colours for the mural. 原文。", Importance: 1, DecayFactor: 1, Metadata: map[string]any{"source": "conversation_segment", "content_hash": "untrusted-metadata-hash"}}
			if err := repo.StoreDocument(ctx, doc); err != nil {
				t.Fatal(err)
			}
			doc, err = repo.GetDocument(ctx, owner, doc.ID, doc.Type)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE memory_generations SET embedding_model='fixture',embedding_version=1 WHERE user_id=$1 AND generation_id=$2`, owner, doc.GenerationID); err != nil {
				t.Fatal(err)
			}
			vectors := vectorstore.NewPGVectorStore(runtimeconfig.NewStaticDBHandle(db))
			if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{UserID: owner, GenerationID: doc.GenerationID, NodeID: doc.NodeID, NodeType: string(doc.Type), EmbeddingVersion: 1, EmbeddingModel: "fixture", Content: doc.Content, Vector: []float32{1, 0, 0}}); err != nil {
				t.Fatal(err)
			}
			switch variant {
			case "changed_content":
				doc.Content = "Mira cancelled the visit. The metadata hash was left unchanged."
			case "missing_vector":
				_, err = db.ExecContext(ctx, `DELETE FROM memory_embeddings WHERE user_id=$1`, owner)
			case "pending":
				_, err = db.ExecContext(ctx, `UPDATE memory_nodes SET embedding_status='pending' WHERE user_id=$1`, owner)
			case "different_generation":
				_, err = db.ExecContext(ctx, `UPDATE memory_embeddings SET generation_id='' WHERE user_id=$1`, owner)
			case "different_model":
				_, err = db.ExecContext(ctx, `UPDATE memory_embeddings SET embedding_model='other-model' WHERE user_id=$1`, owner)
			case "different_version":
				_, err = db.ExecContext(ctx, `UPDATE memory_embeddings SET embedding_version=2 WHERE user_id=$1`, owner)
			case "unverified_hash":
				_, err = db.ExecContext(ctx, `UPDATE memory_embeddings SET content_hash='' WHERE user_id=$1`, owner)
			}
			if err != nil {
				t.Fatal(err)
			}
			doc.Metadata["score_reason"] = "neutral_source_policy"
			if err := repo.StoreDocument(ctx, doc); err != nil {
				t.Fatal(err)
			}
			var status string
			if err := db.QueryRowContext(ctx, `SELECT embedding_status FROM memory_nodes WHERE user_id=$1 AND id=$2`, owner, doc.NodeID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			expected := "pending"
			if variant == "unchanged" {
				expected = "ready"
			}
			if status != expected {
				t.Fatalf("source rewrite status %q, want %q", status, expected)
			}
			found, err := vectors.Search(ctx, domain.VectorSearchInput{UserID: owner, GenerationID: doc.GenerationID, Query: "mural", EmbeddingModel: "fixture", EmbeddingVersion: 1, AllowedTypes: []domain.MemoryType{domain.MemoryTypeSegment}, TopK: 5})
			if err != nil {
				t.Fatal(err)
			}
			if (len(found) == 1) != (expected == "ready") {
				t.Fatalf("status %s yielded %d searchable vectors", expected, len(found))
			}
		})
	}
}
