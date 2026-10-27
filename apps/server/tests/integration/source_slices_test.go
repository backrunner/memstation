package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	ingestionstore "memstation/apps/server/internal/store/sql/ingestion"
	mediastore "memstation/apps/server/internal/store/sql/media"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/service/apikey"
	"memstation/apps/server/internal/service/memorygen"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/service/analysis"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/multimodal"
	vectorstore "memstation/apps/server/internal/store/vector"
	"memstation/apps/server/internal/testutil/memoryllmfixture"
	"memstation/apps/server/internal/worker"
)

type sliceTaskPublisher struct{ tasks []domain.TaskMessage }

func (p *sliceTaskPublisher) Publish(_ context.Context, task domain.TaskMessage) error {
	p.tasks = append(p.tasks, task)
	return nil
}

// Real PostgreSQL, production worker handlers and persisted vector rows; model
// responses/embeddings are deterministic fixtures, not a model-quality benchmark.
func TestLosslessSourceSlicesPostgresWorker(t *testing.T) {
	db := independentDatabase(t)
	ctx := context.Background()
	memories := memorystore.NewPostgresMemoryRepository(db)
	conversations := conversationstore.NewPostgresConversationRepository(db)
	media := mediastore.NewPostgresMediaRepository(db)
	multi := multimodal.NewService(media, nil, bootstrap.MultimodalConfig{})
	vectors := vectorstore.NewPGVectorStore(runtimeconfig.NewStaticDBHandle(db))
	publisher := &sliceTaskPublisher{}
	mux := worker.NewServeMux(slog.New(slog.NewTextHandler(io.Discard, nil)), worker.Services{TaskAuthority: ingestionstore.NewPostgresIngestionRepository(db), Generations: memorygen.NewService(runtimeconfig.NewStaticDBHandle(db), "fixture-embedding"), Conversations: conversations, Memories: memories, Multimodal: multi, Vectors: vectors, Publisher: publisher, Analysis: analysis.NewService(memoryllmfixture.Generator{}), EmbeddingModel: "fixture-embedding"})
	process := func(kind string, payload any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err = json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		fields["schema_version"] = 2
		fields["workspace_id"] = domain.PersonalWorkspaceID(fields["user_id"].(string))
		raw, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if err = mux.ProcessTask(ctx, asynq.NewTask(kind, raw)); err != nil {
			t.Fatal(kind, err)
		}
	}
	const owner = "slice-owner"
	const conv = "slice-conversation"
	agent, agentErr := apikey.NewService(runtimeconfig.NewStaticDBHandle(db)).Identities().Create(ctx, owner, "Source fixture", nil)
	if agentErr != nil {
		t.Fatal(agentErr)
	}
	if _, err := memorygen.NewService(runtimeconfig.NewStaticDBHandle(db), "fixture-embedding").EnsureActiveGeneration(ctx, owner); err != nil {
		t.Fatal(err)
	}
	const tail = "Kestrel cancellation confirmed"
	text := strings.Repeat("ordinary background ", 125) + tail
	now := time.Now().UTC()
	record := domain.ConversationRecord{UserID: owner, ConversationID: conv, RequestFormat: string(domain.ProtocolOpenAIChat), Provider: domain.ProviderOpenAICompatible, Mode: domain.ModeTransparent, FinalizeStatus: domain.FinalizeStatusEnded, SummaryStatus: domain.ProcessingStatusQueued, AnalysisStatus: domain.ProcessingStatusPending, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: text}}, CreatedAt: now, UpdatedAt: now, AgentID: agent.ID}
	record.Metadata = map[string]any{"source_session_time": "2026-10-23T09:00:00Z"}
	if err := conversations.PersistExchange(ctx, record); err != nil {
		t.Fatal(err)
	}
	process(worker.TypeSummaryGenerate, worker.SummaryPayload{UserID: owner, ConversationID: conv, SummaryVersion: 1, FinalizeReason: "explicit"})
	summaries, err := memories.ListDocuments(ctx, owner, []domain.MemoryType{domain.MemoryTypeSummary}, 10)
	if err != nil || len(summaries) != 1 || summaries[0].Metadata["source_session_time"] != "2026-10-23T09:00:00Z" {
		t.Fatal("source session clock not persisted by worker", err)
	}
	for _, task := range publisher.tasks {
		if task.Type == worker.TypeEmbeddingGenerate {
			process(task.Type, task.Payload)
		}
	}
	docs, err := memories.ListDocuments(ctx, owner, []domain.MemoryType{domain.MemoryTypeSegment}, 100)
	if err != nil || len(docs) < 3 {
		t.Fatal("missing long-source chunks", len(docs), err)
	}
	var tailDoc domain.MemoryDocument
	for _, d := range docs {
		if len(d.Content) > 1200 || d.Metadata["source_slices"] == nil || d.Metadata["source_conversation_revision"] == nil {
			t.Fatal("lost bound/provenance")
		}
		if strings.Contains(d.Content, tail) {
			tailDoc = d
		}
	}
	if tailDoc.ID == "" {
		t.Fatal("source tail not persisted")
	}
	var vectorCount int
	if err = db.QueryRow("SELECT count(*) FROM memory_embeddings WHERE node_id=$1", tailDoc.NodeID).Scan(&vectorCount); err != nil || vectorCount != 1 {
		t.Fatal("tail never reached persistent vector store", vectorCount, err)
	}
	evidence, err := memories.ListEvidences(ctx, owner, tailDoc.NodeID)
	if err != nil || len(evidence) == 0 {
		t.Fatal("missing tail evidence", err)
	}
	if !strings.Contains(evidence[len(evidence)-1].ExcerptText, tail) || evidence[len(evidence)-1].Metadata["source_slice"] == nil {
		t.Fatal("tail evidence still quotes source prefix", evidence)
	}
	engine := memoryservice.NewEngine(memories, vectors)
	result, err := engine.Search(ctx, domain.SearchInput{UserID: owner, Query: tail, MemoryTypes: []domain.MemoryType{domain.MemoryTypeSegment}, TopN: 5})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range result.Memories {
		if d.ID == tailDoc.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("tail not returned through shared engine")
	}
	other, err := engine.Search(ctx, domain.SearchInput{UserID: "other-owner", Query: tail, MemoryTypes: []domain.MemoryType{domain.MemoryTypeSegment}, TopN: 5})
	if err != nil || len(other.Memories) != 0 {
		t.Fatal("cross-owner leakage", err)
	}
	// A retry keeps IDs/counts stable, then a shorter source removes old nodes,
	// evidence, graph edges and vectors through the existing cleanup path.
	process(worker.TypeSummaryGenerate, worker.SummaryPayload{UserID: owner, ConversationID: conv, SummaryVersion: 1, FinalizeReason: "explicit"})
	again, err := memories.ListDocuments(ctx, owner, []domain.MemoryType{domain.MemoryTypeSegment}, 100)
	if err != nil || len(again) != len(docs) {
		t.Fatal("retry duplicated slices", err)
	}
	record.Messages = []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "replacement short conversation"}}
	record.UpdatedAt = now.Add(time.Minute)
	if err := conversations.PersistExchange(ctx, record); err != nil {
		t.Fatal(err)
	}
	current, err := conversations.GetConversation(ctx, owner, conv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = multi.MaterializeConversation(ctx, current); err != nil {
		t.Fatal(err)
	}
	process(worker.TypeSummaryGenerate, worker.SummaryPayload{UserID: owner, ConversationID: conv, SummaryVersion: 1, FinalizeReason: "explicit"})
	again, err = memories.ListDocuments(ctx, owner, []domain.MemoryType{domain.MemoryTypeSegment}, 100)
	if err != nil || len(again) != 1 {
		t.Fatal("stale chunks retained", len(again), err)
	}
	for _, table := range []struct{ name, column string }{{"memory_nodes", "id"}, {"memory_evidences", "node_id"}, {"memory_embeddings", "node_id"}} {
		var count int
		if err = db.QueryRow("SELECT count(*) FROM "+table.name+" WHERE "+table.column+"=$1", tailDoc.NodeID).Scan(&count); err != nil || count != 0 {
			t.Fatal("stale tail retained", table.name, count, err)
		}
	}
}
