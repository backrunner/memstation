package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	analysisservice "memstation/apps/server/internal/service/analysis"
	"memstation/apps/server/internal/service/memoryllm"
	"memstation/apps/server/internal/store/sql"
	"memstation/apps/server/internal/testutil/memoryllmfixture"
)

type countingGenerator struct{ calls int }

func (g *countingGenerator) Generate(ctx context.Context, r memoryllm.Request) (json.RawMessage, error) {
	g.calls++
	return (memoryllmfixture.Generator{}).Generate(ctx, r)
}

type fixedGenerator struct {
	raw string
	err error
}

func (g fixedGenerator) Generate(context.Context, memoryllm.Request) (json.RawMessage, error) {
	return json.RawMessage(g.raw), g.err
}

func TestAnalysisWorkerHandlesEmptyResultAndFailureWithoutFabricatedMemories(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		err       error
	}{
		{name: "empty", raw: `{"facts":[],"impression":null,"items":[]}`},
		{name: "unavailable", err: errors.New("upstream unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repo := sql.NewInMemoryMemoryRepository()
			conversations := conversationsFromRecords(t, "owner", "conversation")
			record, err := conversations.GetConversation(ctx, "owner", "conversation")
			if err != nil {
				t.Fatal(err)
			}
			record.Messages = []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "你好"}}
			if err := conversations.PersistExchange(ctx, record); err != nil {
				t.Fatal(err)
			}
			publisher := &recordingPublisher{}
			if err := repo.StoreDocument(ctx, domain.MemoryDocument{ID: "summary", NodeID: "summary-node", Type: domain.MemoryTypeSummary, UserID: "owner", ConversationID: "conversation", Content: "summary"}); err != nil {
				t.Fatal(err)
			}
			services := Services{Conversations: conversations, Memories: repo, Publisher: publisher, Analysis: analysisservice.NewService(fixedGenerator{tc.raw, tc.err})}
			err = handleAnalysisExtract(nilLogger(), services)(ctx, newTask(t, TypeAnalysisExtract, AnalysisPayload{UserID: "owner", ConversationID: "conversation", SummaryIDs: []string{"summary"}}))
			if (err != nil) != (tc.err != nil) {
				t.Fatal(err)
			}
			docs, err := repo.ListDocuments(ctx, "owner", []domain.MemoryType{domain.MemoryTypeFact, domain.MemoryTypeImpression, domain.MemoryTypeTask}, 100)
			if err != nil || len(docs) != 0 {
				t.Fatal("fabricated derived memories", docs, err)
			}
			for _, task := range publisher.tasks {
				if task.Type == TypeEmbeddingGenerate {
					t.Fatal("empty result generated embedding task")
				}
			}
			record, err = conversations.GetConversation(ctx, "owner", "conversation")
			if err != nil {
				t.Fatal(err)
			}
			expected := domain.ProcessingStatusCompleted
			if tc.err != nil {
				expected = domain.ProcessingStatusFailed
			}
			if record.AnalysisStatus != expected {
				t.Fatal(record.AnalysisStatus, expected)
			}
		})
	}
}

func TestValidatedAnalysisCheckpointSurvivesStorageRetry(t *testing.T) {
	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	generator := &countingGenerator{}
	conversation := domain.ConversationRecord{UserID: "owner", ConversationID: "conversation", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "这是确认过的项目事实，不是助手的建议。"}}}
	summary := domain.MemoryDocument{ID: "summary", NodeID: "node-summary", UserID: "owner", ConversationID: "conversation", Type: domain.MemoryTypeSummary, Content: "已确认的项目事实", Metadata: map[string]any{"_llm_summary_checkpoint": newSummaryCheckpoint(conversation, analysisservice.Summary{Summary: "已确认的项目事实", TopicLabel: "项目", Keywords: []string{"项目"}, Importance: 1, DecayFactor: 1, ChunkCount: 1})}}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatal(err)
	}
	services := Services{Memories: repo, Analysis: analysisservice.NewService(generator)}
	input := analysisservice.Input{UserID: "owner", ConversationID: "conversation", Conversation: conversation, Summary: summary}
	first, err := analyzeWithCheckpoint(ctx, services, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeSummaryWithCheckpoint(ctx, services, summary); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetDocument(ctx, "owner", "summary", domain.MemoryTypeSummary)
	if err != nil {
		t.Fatal(err)
	}
	input.Summary = stored
	second, err := analyzeWithCheckpoint(ctx, services, input)
	if err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 || first.Fact.ID != second.Fact.ID {
		t.Fatal("storage retry regenerated semantic output", generator.calls, first.Fact.ID, second.Fact.ID)
	}
	public, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "_llm_") {
		t.Fatal("internal checkpoint exposed in public memory JSON")
	}
	quotes := second.Fact.Metadata["source_quotes"].([]any)
	if intValue(quotes[0].(map[string]any), "sequence_num") != 1 {
		t.Fatal("checkpoint lost evidence sequence")
	}
}

func TestKeyMemoryRetryReusesOutputAndPreservesConcurrentEdits(t *testing.T) {
	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	generator := &countingGenerator{}
	docs := []domain.MemoryDocument{
		{ID: "summary", NodeID: "summary-node", Type: domain.MemoryTypeSummary, UserID: "owner", ConversationID: "conversation", Content: "summary"},
		{ID: "fact", NodeID: "fact-node", Type: domain.MemoryTypeFact, UserID: "owner", ConversationID: "conversation", Content: "model source"},
	}
	for _, doc := range docs {
		if err := repo.StoreDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	services := Services{Memories: repo, Conversations: conversationsFromRecords(t, "owner", "conversation"), Analysis: analysisservice.NewService(generator), AllowedKeyMemoryKeys: map[string]struct{}{"preferences": {}}}
	task := newTask(t, TypeKeyMemoryReconcile, KeyMemoryReconcilePayload{UserID: "owner", ConversationID: "conversation"})
	handler := handleKeyMemoryReconcile(nilLogger(), services)
	if err := handler(ctx, task); err != nil {
		t.Fatal(err)
	}
	first, exists, err := repo.GetKeyMemory(ctx, "owner", "preferences")
	if err != nil || !exists {
		t.Fatal(first, exists, err)
	}
	if err := handler(ctx, task); err != nil {
		t.Fatal(err)
	}
	if generator.calls != 1 {
		t.Fatal("retry called model again", generator.calls)
	}
	first.Value = "manually corrected"
	first.Metadata = map[string]any{}
	if err := repo.UpsertKeyMemory(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := handler(ctx, task); !errors.Is(err, domain.ErrAccessConflict) {
		t.Fatal("concurrent manual edit overwritten", err)
	}
	current, _, _ := repo.GetKeyMemory(ctx, "owner", "preferences")
	if current.Value != "manually corrected" {
		t.Fatal(current)
	}
}

type competingAnalysisGenerator struct {
	calls   atomic.Int32
	arrived chan struct{}
	release chan struct{}
}

func (g *competingAnalysisGenerator) Generate(ctx context.Context, r memoryllm.Request) (json.RawMessage, error) {
	count := g.calls.Add(1)
	g.arrived <- struct{}{}
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	raw, err := (memoryllmfixture.Generator{}).Generate(ctx, r)
	if err != nil {
		return nil, err
	}
	var extraction analysisservice.Extraction
	if err := json.Unmarshal(raw, &extraction); err != nil {
		return nil, err
	}
	extraction.Facts[0].Content = fmt.Sprintf("Model paraphrase %d", count)
	return json.Marshal(extraction)
}

func TestConcurrentAnalysisAdoptsOnePersistedResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	repo := sql.NewInMemoryMemoryRepository()
	conversation := domain.ConversationRecord{UserID: "owner", ConversationID: "conversation", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "This is the source assertion."}}}
	summary := domain.MemoryDocument{ID: "summary", UserID: "owner", ConversationID: "conversation", Type: domain.MemoryTypeSummary, Content: "summary"}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatal(err)
	}
	summary, err := repo.GetDocument(ctx, "owner", "summary", domain.MemoryTypeSummary)
	if err != nil {
		t.Fatal(err)
	}
	generator := &competingAnalysisGenerator{arrived: make(chan struct{}, 2), release: make(chan struct{})}
	services := Services{Memories: repo, Analysis: analysisservice.NewService(generator)}
	type output struct {
		result analysisservice.Result
		err    error
	}
	results := make(chan output, 2)
	for range 2 {
		go func() {
			result, err := analyzeWithCheckpoint(ctx, services, analysisservice.Input{UserID: "owner", ConversationID: "conversation", Conversation: conversation, Summary: summary})
			results <- output{result, err}
		}()
	}
	for range 2 {
		select {
		case <-generator.arrived:
		case <-ctx.Done():
			t.Fatal("model calls did not overlap")
		}
	}
	close(generator.release)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.result.Fact.ID != second.result.Fact.ID {
		t.Fatal("competing tasks produced different persisted identities", first.err, second.err, first.result.Fact.ID, second.result.Fact.ID)
	}
}
