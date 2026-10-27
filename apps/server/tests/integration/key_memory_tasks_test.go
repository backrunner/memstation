package integration_test

import (
	"context"
	"testing"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	ingestionstore "memstation/apps/server/internal/store/sql/ingestion"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/apikey"
	vectorstore "memstation/apps/server/internal/store/vector"
)

func TestKeyMemoryTasksKeepScopeAndStaleDeletionCannotEraseRecreatedVector(t *testing.T) {
	db := independentDatabase(t)
	const owner = "key-task-owner"
	agent, err := apikey.NewService(runtimeconfig.NewStaticDBHandle(db)).Identities().Create(context.Background(), owner, "Key memory fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := security.WithDataScope(context.Background(), owner, agent.DefaultWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	memories := memorystore.NewPostgresMemoryRepository(db)
	vectors := vectorstore.NewPGVectorStore(runtimeconfig.NewStaticDBHandle(db))
	authority := ingestionstore.NewPostgresIngestionRepository(db)
	key := domain.KeyMemory{UserID: owner, Key: "editor", Value: "original"}
	if err = memories.UpsertKeyMemory(ctx, key); err != nil {
		t.Fatal(err)
	}
	var nodeID string
	var revision int64
	load := func() {
		t.Helper()
		if err = db.QueryRowContext(ctx, `SELECT n.id,n.embedding_revision FROM memory_nodes n JOIN key_memories k ON k.node_id=n.id WHERE k.user_id=$1 AND k.key=$2`, owner, key.Key).Scan(&nodeID, &revision); err != nil {
			t.Fatal(err)
		}
	}
	load()
	pending, err := conversationstore.NewPostgresOutboxRepository(db).Pending(ctx, 10)
	if err != nil || len(pending) != 2 {
		t.Fatal("expected embedding and graph tasks", len(pending), err)
	}
	for _, event := range pending {
		id, _ := event.Payload["node_id"].(string)
		if id != nodeID {
			t.Fatal("task lacks authoritative node", event.EventType)
		}
		if err = authority.ValidateMemoryTask(ctx, domain.MemoryTaskScope{UserID: owner, WorkspaceID: agent.DefaultWorkspaceID, NodeID: id}); err != nil {
			t.Fatal("current task scope rejected", event.EventType, err)
		}
	}
	if err = memories.DeleteKeyMemory(ctx, owner, key.Key); err != nil {
		t.Fatal(err)
	}
	load()
	deletedNode, deletedRevision := nodeID, revision
	// A new write and embedding commit before the delayed old delete executes.
	key.Value = "recreated"
	if err = memories.UpsertKeyMemory(ctx, key); err != nil {
		t.Fatal(err)
	}
	load()
	if nodeID != deletedNode || revision <= deletedRevision {
		t.Fatal("fixture did not recreate the same node with a newer revision")
	}
	if err = vectors.Upsert(ctx, domain.VectorUpsertRequest{UserID: owner, NodeID: nodeID, NodeType: "key_memory", EmbeddingRevision: revision, EmbeddingVersion: 1, EmbeddingModel: "fixture", Vector: []float32{1, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	assertVector := func() {
		t.Helper()
		var count int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM memory_embeddings WHERE user_id=$1 AND node_id=$2`, owner, nodeID).Scan(&count); err != nil || count != 1 {
			t.Fatal("new vector erased", count, err)
		}
	}
	if err = vectors.DeleteByNode(ctx, domain.VectorDeleteRequest{UserID: owner, NodeID: nodeID, EmbeddingRevision: deletedRevision}); err != nil {
		t.Fatal(err)
	}
	assertVector()
	if err = vectors.DeleteByNode(ctx, domain.VectorDeleteRequest{UserID: owner, NodeID: nodeID, RequireMissingNode: true}); err != nil {
		t.Fatal(err)
	}
	assertVector()
	if err = vectors.DeleteByNode(ctx, domain.VectorDeleteRequest{UserID: owner, NodeID: nodeID}); err == nil {
		t.Fatal("unconditioned deletion accepted")
	}
	// The persisted terminal task carries its deletion revision, not an inferred one.
	pending, err = conversationstore.NewPostgresOutboxRepository(db).Pending(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range pending {
		if event.EventType == domain.TaskTypeEmbeddingDelete {
			found = true
			if event.Payload["embedding_revision"] != float64(deletedRevision) {
				t.Fatal("delete task omitted revision", event.Payload)
			}
		}
	}
	if !found {
		t.Fatal("delete task missing")
	}
}
