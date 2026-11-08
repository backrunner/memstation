package eval

import (
	"context"
	"os"
	"testing"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	memorystore "memstation/apps/server/internal/store/sql/memory"
	vectorstore "memstation/apps/server/internal/store/vector"

	"github.com/google/uuid"
)

func TestPostgresSourceReadinessRejectsUnqueryableCorpus(t *testing.T) {
	dsn := os.Getenv("MEMSTATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MEMSTATION_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	// This synthetic contract test uses the configured disposable integration DB;
	// manual model-backed evaluations retain their stricter database guard.
	db, err := bootstrap.OpenPostgres(ctx, bootstrap.Config{DatabaseURL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user := "source-gate-" + uuid.NewString()
	handle := runtimeconfig.NewStaticDBHandle(db)
	generations := memorygenservice.NewService(handle, "readiness-fixture")
	g, err := generations.EnsureActiveGeneration(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	repo := memorystore.NewPostgresMemoryRepository(db)
	doc := domain.MemoryDocument{ID: uuid.NewString(), UserID: user, GenerationID: g.GenerationID, Type: domain.MemoryTypeSegment, Content: "The original source remains available."}
	if err := repo.StoreDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	doc, err = repo.GetDocument(ctx, user, doc.ID, doc.Type)
	if err != nil {
		t.Fatal(err)
	}
	vectors := vectorstore.NewPGVectorStore(handle)
	embed := func() {
		t.Helper()
		if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{UserID: user, GenerationID: g.GenerationID, NodeID: doc.NodeID, NodeType: string(doc.Type), EmbeddingVersion: g.EmbeddingVersion, EmbeddingModel: g.EmbeddingModel, Content: doc.Content, Vector: []float32{1, 0, 0}}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(model string, version int, want bool) {
		t.Helper()
		err := requireQueryableSourceIndex(ctx, db, user, "", g.GenerationID, model, version)
		if (err == nil) != want {
			t.Fatalf("readiness success=%t, want %t: %v", err == nil, want, err)
		}
	}
	check(g.EmbeddingModel, g.EmbeddingVersion, false)
	embed()
	check(g.EmbeddingModel, g.EmbeddingVersion, true)
	check("wrong-model", g.EmbeddingVersion, false)
	check(g.EmbeddingModel, g.EmbeddingVersion+1, false)
	if _, err := db.ExecContext(ctx, `UPDATE memory_nodes SET embedding_status='pending' WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	check(g.EmbeddingModel, g.EmbeddingVersion, false)
	embed()
	if _, err := db.ExecContext(ctx, `UPDATE conversation_segments SET content='Changed source' WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	check(g.EmbeddingModel, g.EmbeddingVersion, false)
	if err := requireQueryableSourceIndex(ctx, db, "missing-owner", "", g.GenerationID, g.EmbeddingModel, g.EmbeddingVersion); err == nil {
		t.Fatal("empty corpus accepted")
	}
}
