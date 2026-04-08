package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
	analysisservice "memstation/apps/server/internal/service/analysis"
	conversationservice "memstation/apps/server/internal/service/conversation"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	"memstation/apps/server/internal/store/sql"
	"memstation/apps/server/internal/store/vector"
)

type recordingPublisher struct {
	tasks []domain.TaskMessage
}

func (p *recordingPublisher) Publish(_ context.Context, task domain.TaskMessage) error {
	p.tasks = append(p.tasks, task)
	return nil
}

type recordingVectorStore struct {
	lastUpsert domain.VectorUpsertRequest
	upserts    []domain.VectorUpsertRequest
}

func (s *recordingVectorStore) Search(context.Context, domain.VectorSearchInput) ([]domain.VectorSearchResult, error) {
	return nil, nil
}

func (s *recordingVectorStore) Upsert(_ context.Context, input domain.VectorUpsertRequest) error {
	s.lastUpsert = input
	s.upserts = append(s.upserts, input)
	return nil
}

func (s *recordingVectorStore) DeleteByNode(context.Context, string, string) error {
	return nil
}

func (s *recordingVectorStore) Health(context.Context) error {
	return nil
}

func (s *recordingVectorStore) ReindexVersion(context.Context, int) error {
	return nil
}

func TestConversationFinalizeHandlerTransitionsToFinalAndQueuesSummary(t *testing.T) {
	t.Parallel()
	conversations := sql.NewInMemoryConversationRepository()
	publisher := &recordingPublisher{}

	if err := conversations.PersistExchange(context.Background(), domain.ConversationRecord{
		ConversationID: "conv-1",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusActive,
		SummaryStatus:  domain.ProcessingStatusPending,
		AnalysisStatus: domain.ProcessingStatusPending,
		Messages: []domain.UnifiedMessage{{
			Role: domain.RoleUser,
			Text: "hello",
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}

	handler := handleConversationFinalize(nilLogger(), Services{
		Conversations: conversations,
		Publisher:     publisher,
	})
	task := newTask(t, TypeConversationFinalize, map[string]any{
		"conversation_id": "conv-1",
		"user_id":         "user-1",
		"finalize_reason": "explicit",
		"finalize_status": string(domain.FinalizeStatusEnded),
	})
	if err := handler(context.Background(), task); err != nil {
		t.Fatalf("handle finalize: %v", err)
	}

	conversation, err := conversations.GetConversation(context.Background(), "user-1", "conv-1")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if conversation.FinalizeStatus != domain.FinalizeStatusEnded {
		t.Fatalf("expected ended status, got %s", conversation.FinalizeStatus)
	}
	if conversation.SummaryStatus != domain.ProcessingStatusQueued {
		t.Fatalf("expected queued summary status, got %s", conversation.SummaryStatus)
	}
	if len(publisher.tasks) != 1 || publisher.tasks[0].Type != TypeSummaryGenerate {
		t.Fatalf("expected summary task to be queued, got %+v", publisher.tasks)
	}
}

func TestSummaryGenerateHandlerIsDeterministicAndMarksCompleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	conversations := sql.NewInMemoryConversationRepository()
	memories := sql.NewInMemoryMemoryRepository()
	publisher := &recordingPublisher{}

	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-1",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusQueued,
		AnalysisStatus: domain.ProcessingStatusPending,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "hello"},
			{Role: domain.RoleAssistant, Text: "hi there"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}

	handler := handleSummaryGenerate(nilLogger(), Services{
		Conversations:  conversations,
		Memories:       memories,
		Vectors:        vector.NewPGVectorStore(nil),
		Publisher:      publisher,
		EmbeddingModel: "test-embedding",
	})
	task := newTask(t, TypeSummaryGenerate, SummaryPayload{
		ConversationID: "conv-1",
		UserID:         "user-1",
		SummaryVersion: 1,
		FinalizeReason: "explicit",
	})
	if err := handler(ctx, task); err != nil {
		t.Fatalf("handle summary: %v", err)
	}
	if err := handler(ctx, task); err != nil {
		t.Fatalf("handle summary second run: %v", err)
	}

	docs, err := memories.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeSummary}, 10)
	if err != nil {
		t.Fatalf("list docs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected exactly one summary doc, got %d", len(docs))
	}
	if docs[0].ID != "summary:conv-1:v1" {
		t.Fatalf("unexpected summary id: %s", docs[0].ID)
	}
	if got := docs[0].Metadata["segment_count"]; got != 1 {
		t.Fatalf("expected single segment metadata, got %v", got)
	}
	conversation, err := conversations.GetConversation(ctx, "user-1", "conv-1")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if conversation.SummaryStatus != domain.ProcessingStatusCompleted {
		t.Fatalf("expected completed summary status, got %s", conversation.SummaryStatus)
	}
	if conversation.AnalysisStatus != domain.ProcessingStatusQueued {
		t.Fatalf("expected queued analysis status, got %s", conversation.AnalysisStatus)
	}
	if len(conversation.Segments) != 1 {
		t.Fatalf("expected persisted conversation segments, got %d", len(conversation.Segments))
	}
	if conversation.Segments[0].SegmentIndex != 0 || conversation.Segments[0].MessageCount != 2 {
		t.Fatalf("unexpected first segment: %+v", conversation.Segments[0])
	}
	foundSummaryEmbeddingTask := false
	for _, queued := range publisher.tasks {
		if queued.Type != TypeEmbeddingGenerate {
			continue
		}
		content, _ := queued.Payload["content"].(string)
		if strings.Contains(content, "Keywords:") {
			foundSummaryEmbeddingTask = true
			break
		}
	}
	if !foundSummaryEmbeddingTask {
		t.Fatalf("expected summary embedding task content to include keywords, got %+v", publisher.tasks)
	}
}

func TestSummaryGenerateHandlerStoresSegmentGraphAndEmbeddings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := sql.NewInMemoryConversationRepository()
	memories := sql.NewInMemoryMemoryRepository()
	publisher := &recordingPublisher{}

	messages := []domain.UnifiedMessage{
		{Role: domain.RoleUser, Text: "I prefer structured rollout plans."},
		{Role: domain.RoleAssistant, Text: "Noted, we will keep the rollout checklist explicit."},
		{Role: domain.RoleUser, Text: "Please also remember the rollback owner is Alice."},
		{Role: domain.RoleAssistant, Text: "Stored, rollback owner is Alice."},
		{Role: domain.RoleUser, Text: "We should stage the migration in small batches."},
		{Role: domain.RoleAssistant, Text: "Agreed, we will use small migration batches."},
		{Role: domain.RoleUser, Text: "Can we keep the deployment playbook nearby?"},
		{Role: domain.RoleAssistant, Text: "Yes, I will keep the deployment playbook in context."},
	}

	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-segments",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusQueued,
		AnalysisStatus: domain.ProcessingStatusPending,
		Messages:       messages,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}

	handler := handleSummaryGenerate(nilLogger(), Services{
		Conversations:  conversations,
		Memories:       memories,
		Vectors:        vector.NewPGVectorStore(nil),
		Publisher:      publisher,
		EmbeddingModel: "test-embedding",
	})
	if err := handler(ctx, newTask(t, TypeSummaryGenerate, SummaryPayload{
		ConversationID: "conv-segments",
		UserID:         "user-1",
		SummaryVersion: 1,
		FinalizeReason: "explicit",
	})); err != nil {
		t.Fatalf("handle summary with multi segment conversation: %v", err)
	}

	segments, err := memories.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeSegment}, 10)
	if err != nil {
		t.Fatalf("list segment docs: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("expected 2 segment docs, got %d", len(segments))
	}

	summaryNodeID := "node:summary:conv-segments:v1"
	secondSegmentNodeID := segmentNodeID("segment:conv-segments:001")
	relations, err := memories.ListRelations(ctx, "user-1", "", []string{secondSegmentNodeID}, 1)
	if err != nil {
		t.Fatalf("list segment relations: %v", err)
	}
	foundSummaryLink := false
	foundFollowsLink := false
	for _, relation := range relations {
		switch relation.RelationType {
		case "summarized_into":
			if relation.SrcNodeID == secondSegmentNodeID && relation.DstNodeID == summaryNodeID {
				foundSummaryLink = true
			}
		case "follows_segment":
			if relation.SrcNodeID == secondSegmentNodeID && relation.DstNodeID == segmentNodeID("segment:conv-segments:000") {
				foundFollowsLink = true
			}
		}
	}
	if !foundSummaryLink {
		t.Fatalf("expected second segment to link to summary node %s, got %+v", summaryNodeID, relations)
	}
	if !foundFollowsLink {
		t.Fatalf("expected ordered segment relation, got %+v", relations)
	}

	embeddingTasks := 0
	segmentEmbeddingTasks := 0
	for _, queued := range publisher.tasks {
		if queued.Type != TypeEmbeddingGenerate {
			continue
		}
		embeddingTasks++
		if queued.Payload["node_type"] == string(domain.MemoryTypeSegment) {
			segmentEmbeddingTasks++
		}
	}
	if embeddingTasks != 3 {
		t.Fatalf("expected summary + 2 segment embedding tasks, got %d (%+v)", embeddingTasks, publisher.tasks)
	}
	if segmentEmbeddingTasks != 2 {
		t.Fatalf("expected 2 segment embedding tasks, got %d", segmentEmbeddingTasks)
	}
}

func TestSummaryGenerateHandlerRemovesStaleSegmentsAfterRewrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := sql.NewInMemoryConversationRepository()
	memories := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	publisher := &recordingPublisher{}

	initialMessages := []domain.UnifiedMessage{
		{Role: domain.RoleUser, Text: "I like explicit rollout plans."},
		{Role: domain.RoleAssistant, Text: "Understood, explicit rollout plans are preferred."},
		{Role: domain.RoleUser, Text: "Please remember the rollback owner is Alice."},
		{Role: domain.RoleAssistant, Text: "Stored, rollback owner is Alice."},
		{Role: domain.RoleUser, Text: "Keep the deployment playbook visible."},
		{Role: domain.RoleAssistant, Text: "Acknowledged, the deployment playbook stays visible."},
		{Role: domain.RoleUser, Text: "Migrate in small batches."},
		{Role: domain.RoleAssistant, Text: "We'll migrate in small batches."},
	}

	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-rewrite-segments",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusQueued,
		AnalysisStatus: domain.ProcessingStatusPending,
		Messages:       initialMessages,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist initial conversation: %v", err)
	}

	handler := handleSummaryGenerate(nilLogger(), Services{
		Conversations:  conversations,
		Memories:       memories,
		Vectors:        vectors,
		Publisher:      publisher,
		EmbeddingModel: "test-embedding",
	})
	task := newTask(t, TypeSummaryGenerate, SummaryPayload{
		ConversationID: "conv-rewrite-segments",
		UserID:         "user-1",
		SummaryVersion: 1,
		FinalizeReason: "explicit",
	})
	if err := handler(ctx, task); err != nil {
		t.Fatalf("initial summary run: %v", err)
	}

	segments, err := memories.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeSegment}, 10)
	if err != nil {
		t.Fatalf("list initial segments: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("expected 2 initial segments, got %d", len(segments))
	}

	var staleSegment domain.MemoryDocument
	for _, item := range segments {
		if item.Metadata["segment_index"] == 1 {
			staleSegment = item
			break
		}
	}
	if staleSegment.ID == "" {
		t.Fatalf("expected to locate stale segment candidate, got %+v", segments)
	}
	if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
		UserID:           staleSegment.UserID,
		NodeID:           staleSegment.NodeID,
		NodeType:         string(staleSegment.Type),
		EmbeddingVersion: 1,
		EmbeddingModel:   "test-embedding",
		Content:          staleSegment.Content,
	}); err != nil {
		t.Fatalf("seed stale segment embedding: %v", err)
	}

	rewrittenMessages := []domain.UnifiedMessage{
		{Role: domain.RoleUser, Text: "I only need a shorter conversation now."},
		{Role: domain.RoleAssistant, Text: "Understood, this replaces the earlier longer exchange."},
	}
	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-rewrite-segments",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusQueued,
		AnalysisStatus: domain.ProcessingStatusPending,
		Messages:       rewrittenMessages,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC().Add(time.Minute),
	}); err != nil {
		t.Fatalf("persist rewritten shorter conversation: %v", err)
	}

	if err := handler(ctx, task); err != nil {
		t.Fatalf("summary run after rewrite: %v", err)
	}

	segments, err = memories.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeSegment}, 10)
	if err != nil {
		t.Fatalf("list rewritten segments: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected rewritten conversation to leave 1 segment, got %d", len(segments))
	}

	if _, err := memories.GetDocument(ctx, "user-1", staleSegment.ID, domain.MemoryTypeSegment); err == nil {
		t.Fatalf("expected stale segment %s to be removed after rewrite", staleSegment.ID)
	}

	relations, err := memories.ListRelations(ctx, "user-1", "", []string{staleSegment.NodeID}, 1)
	if err != nil {
		t.Fatalf("list stale segment relations: %v", err)
	}
	if len(relations) != 0 {
		t.Fatalf("expected stale segment relations to be removed, got %+v", relations)
	}

	vectorResults, err := vectors.Search(ctx, domain.VectorSearchInput{
		UserID:           "user-1",
		Query:            staleSegment.Content,
		TopK:             5,
		AllowedTypes:     []domain.MemoryType{domain.MemoryTypeSegment},
		EmbeddingVersion: 1,
	})
	if err != nil {
		t.Fatalf("search stale segment vector: %v", err)
	}
	for _, item := range vectorResults {
		if item.NodeID == staleSegment.NodeID {
			t.Fatalf("expected stale segment vector %s to be removed, got %+v", staleSegment.NodeID, vectorResults)
		}
	}
}

func TestSummaryGenerateHandlerRemovesStaleSegmentsAfterSideTableRewrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := sql.NewInMemoryConversationRepository()
	memories := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)
	publisher := &recordingPublisher{}
	now := time.Now().UTC()

	currentMessages := []domain.UnifiedMessage{
		{Role: domain.RoleUser, Text: "We only need the short version now."},
		{Role: domain.RoleAssistant, Text: "Understood, the conversation history is now short."},
	}
	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-side-table-rewrite",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusQueued,
		AnalysisStatus: domain.ProcessingStatusPending,
		Messages:       currentMessages,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("persist rewritten conversation: %v", err)
	}

	currentSegments := conversationservice.BuildSegments("user-1", "conv-side-table-rewrite", currentMessages, now, conversationservice.DefaultSegmentOptions())
	if len(currentSegments) != 1 {
		t.Fatalf("expected current conversation to build one segment, got %d", len(currentSegments))
	}
	if err := conversations.ReplaceSegments(ctx, "user-1", "conv-side-table-rewrite", currentSegments); err != nil {
		t.Fatalf("replace current segments: %v", err)
	}

	staleSegment := domain.ConversationSegment{
		ID:                  "segment:conv-side-table-rewrite:001",
		UserID:              "user-1",
		ConversationID:      "conv-side-table-rewrite",
		SegmentIndex:        1,
		StartSequence:       2,
		EndSequence:         7,
		MessageCount:        6,
		OverlapFromSequence: 4,
		OverlapMessageCount: 2,
		Content:             "user: stale historical segment | assistant: stale reply",
		ContentHash:         "stale-segment",
		Metadata:            map[string]any{"source": "test"},
		CreatedAt:           now.Add(-time.Hour),
		UpdatedAt:           now.Add(-time.Hour),
	}
	staleDoc := segmentDocument(staleSegment)
	if err := memories.StoreDocument(ctx, staleDoc); err != nil {
		t.Fatalf("store stale segment doc: %v", err)
	}
	if err := memories.LinkDocuments(ctx, domain.MemoryRelation{
		UserID:       "user-1",
		SrcNodeID:    staleDoc.NodeID,
		DstNodeID:    "node:summary:conv-side-table-rewrite:v1",
		RelationType: "summarized_into",
		Weight:       0.9,
		Source:       "test",
	}); err != nil {
		t.Fatalf("link stale segment relation: %v", err)
	}
	if err := vectors.Upsert(ctx, domain.VectorUpsertRequest{
		UserID:           staleDoc.UserID,
		NodeID:           staleDoc.NodeID,
		NodeType:         string(staleDoc.Type),
		EmbeddingVersion: 1,
		EmbeddingModel:   "test-embedding",
		Content:          staleDoc.Content,
	}); err != nil {
		t.Fatalf("seed stale segment vector: %v", err)
	}

	conversation, err := conversations.GetConversation(ctx, "user-1", "conv-side-table-rewrite")
	if err != nil {
		t.Fatalf("get conversation before summary rerun: %v", err)
	}
	if len(conversation.Segments) != 1 || conversation.Segments[0].ID != currentSegments[0].ID {
		t.Fatalf("expected side table to already reflect current segments only, got %+v", conversation.Segments)
	}

	handler := handleSummaryGenerate(nilLogger(), Services{
		Conversations:  conversations,
		Memories:       memories,
		Vectors:        vectors,
		Publisher:      publisher,
		EmbeddingModel: "test-embedding",
	})
	if err := handler(ctx, newTask(t, TypeSummaryGenerate, SummaryPayload{
		ConversationID: "conv-side-table-rewrite",
		UserID:         "user-1",
		SummaryVersion: 1,
		FinalizeReason: "explicit",
	})); err != nil {
		t.Fatalf("summary rerun after side table rewrite: %v", err)
	}

	if _, err := memories.GetDocument(ctx, "user-1", staleDoc.ID, domain.MemoryTypeSegment); err == nil {
		t.Fatalf("expected stale segment %s to be removed even when side table already moved on", staleDoc.ID)
	}
	relations, err := memories.ListRelations(ctx, "user-1", "", []string{staleDoc.NodeID}, 1)
	if err != nil {
		t.Fatalf("list stale segment relations: %v", err)
	}
	if len(relations) != 0 {
		t.Fatalf("expected stale segment relations to be removed, got %+v", relations)
	}
	vectorResults, err := vectors.Search(ctx, domain.VectorSearchInput{
		UserID:           "user-1",
		Query:            staleDoc.Content,
		TopK:             5,
		AllowedTypes:     []domain.MemoryType{domain.MemoryTypeSegment},
		EmbeddingVersion: 1,
	})
	if err != nil {
		t.Fatalf("search stale segment vector: %v", err)
	}
	for _, item := range vectorResults {
		if item.NodeID == staleDoc.NodeID {
			t.Fatalf("expected stale segment vector %s to be removed, got %+v", staleDoc.NodeID, vectorResults)
		}
	}
}

func TestKeyMemoryReconcileHandlerEnforcesWhitelistAndAudits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	memories := sql.NewInMemoryMemoryRepository()
	var audits []domain.AuditEvent
	var invalidated []string

	for _, doc := range []domain.MemoryDocument{
		{
			ID:             "summary:conv-1:v1",
			NodeID:         "node:summary:conv-1:v1",
			UserID:         "user-1",
			ConversationID: "conv-1",
			Type:           domain.MemoryTypeSummary,
			Content:        "Conversation summary for key-memory reconciliation.",
		},
		{
			ID:             "fact:conv-1:owner",
			NodeID:         "node:fact:conv-1:owner",
			UserID:         "user-1",
			ConversationID: "conv-1",
			Type:           domain.MemoryTypeFact,
			Content:        "Rollback owner is Alice.",
		},
	} {
		if err := memories.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("seed memory document %s: %v", doc.ID, err)
		}
	}

	denied := handleKeyMemoryReconcile(nilLogger(), Services{
		Memories: memories,
	})
	if err := denied(ctx, newTask(t, TypeKeyMemoryReconcile, map[string]any{
		"user_id":         "user-1",
		"conversation_id": "conv-1",
	})); err == nil {
		t.Fatal("expected reconcile to fail when whitelist is empty")
	}

	allowed := handleKeyMemoryReconcile(nilLogger(), Services{
		Memories: memories,
		AllowedKeyMemoryKeys: map[string]struct{}{
			"recent_conversation": {},
		},
		RecordAudit: func(_ context.Context, event domain.AuditEvent) {
			audits = append(audits, event)
		},
		InvalidateKeyMemory: func(_ context.Context, userID string) {
			invalidated = append(invalidated, userID)
		},
	})
	if err := allowed(ctx, newTask(t, TypeKeyMemoryReconcile, map[string]any{
		"user_id":         "user-1",
		"conversation_id": "conv-1",
	})); err != nil {
		t.Fatalf("handle key memory: %v", err)
	}
	memoriesList, err := memories.ListKeyMemories(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list key memories: %v", err)
	}
	if len(memoriesList) != 1 {
		t.Fatalf("expected one key memory, got %d", len(memoriesList))
	}
	if len(audits) != 1 || audits[0].Action != "keymemory_reconcile" {
		t.Fatalf("expected audit event to be recorded, got %+v", audits)
	}
	if len(invalidated) != 1 || invalidated[0] != "user-1" {
		t.Fatalf("expected cache invalidation callback for user-1, got %+v", invalidated)
	}
	relations, err := memories.ListRelations(ctx, "user-1", "", []string{keyMemoryNodeID("user-1", "recent_conversation")}, 1)
	if err != nil {
		t.Fatalf("list key memory relations: %v", err)
	}
	promoted := make([]domain.MemoryRelation, 0, len(relations))
	for _, relation := range relations {
		if relation.RelationType == "promoted_to_key_memory" {
			promoted = append(promoted, relation)
		}
	}
	if len(promoted) != 2 {
		t.Fatalf("expected 2 promoted key memory relations, got %d", len(promoted))
	}
	for _, relation := range promoted {
		if relation.RelationType != "promoted_to_key_memory" {
			t.Fatalf("unexpected relation type: %+v", relation)
		}
		if relation.DstNodeID != keyMemoryNodeID("user-1", "recent_conversation") {
			t.Fatalf("expected key memory node as relation target, got %+v", relation)
		}
	}
}

func TestKeyMemoryReconcileHandlerScopesRelationsToRequestedGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	memories := sql.NewInMemoryMemoryRepository()
	currentGenerationID := memorygenservice.CurrentGenerationID("user-1")
	candidateGenerationID := "gen:rebuild:user-1"

	for _, doc := range []domain.MemoryDocument{
		{
			ID:             currentGenerationID + ":summary:conv-1:v1",
			NodeID:         currentGenerationID + ":node:summary:conv-1:v1",
			GenerationID:   currentGenerationID,
			UserID:         "user-1",
			ConversationID: "conv-1",
			Type:           domain.MemoryTypeSummary,
			Content:        "Current generation summary that should stay isolated.",
		},
		{
			ID:             candidateGenerationID + ":summary:conv-1:v1",
			NodeID:         candidateGenerationID + ":node:summary:conv-1:v1",
			GenerationID:   candidateGenerationID,
			UserID:         "user-1",
			ConversationID: "conv-1",
			Type:           domain.MemoryTypeSummary,
			Content:        "Candidate generation summary that should be linked.",
		},
	} {
		if err := memories.StoreDocument(ctx, doc); err != nil {
			t.Fatalf("store generation-scoped document %s: %v", doc.ID, err)
		}
	}

	handler := handleKeyMemoryReconcile(nilLogger(), Services{
		Memories: memories,
		AllowedKeyMemoryKeys: map[string]struct{}{
			"recent_conversation": {},
		},
	})
	if err := handler(ctx, newTask(t, TypeKeyMemoryReconcile, KeyMemoryReconcilePayload{
		UserID:         "user-1",
		ConversationID: "conv-1",
		GenerationID:   candidateGenerationID,
	})); err != nil {
		t.Fatalf("handle generation-scoped key memory reconcile: %v", err)
	}

	keyNodeID := keyMemoryNodeID("user-1", "recent_conversation")
	relations, err := memories.ListRelations(ctx, "user-1", candidateGenerationID, []string{keyNodeID}, 1)
	if err != nil {
		t.Fatalf("list candidate generation relations: %v", err)
	}
	promoted := make([]domain.MemoryRelation, 0, len(relations))
	for _, relation := range relations {
		if relation.RelationType == "promoted_to_key_memory" {
			promoted = append(promoted, relation)
		}
	}
	if len(promoted) != 1 {
		t.Fatalf("expected 1 candidate-generation promoted relation, got %+v", relations)
	}
	if promoted[0].GenerationID != candidateGenerationID {
		t.Fatalf("expected relation generation %s, got %s", candidateGenerationID, promoted[0].GenerationID)
	}
	if promoted[0].SrcNodeID != candidateGenerationID+":node:summary:conv-1:v1" {
		t.Fatalf("expected candidate summary relation source, got %+v", promoted[0])
	}

	currentRelations, err := memories.ListRelations(ctx, "user-1", currentGenerationID, []string{keyNodeID}, 1)
	if err != nil {
		t.Fatalf("list current generation relations: %v", err)
	}
	for _, relation := range currentRelations {
		if relation.RelationType == "promoted_to_key_memory" {
			t.Fatalf("expected no current-generation promoted relations, got %+v", currentRelations)
		}
	}
}

func TestAnalysisExtractHandlerStoresDerivedDocumentsAndFollowups(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := sql.NewInMemoryConversationRepository()
	memories := sql.NewInMemoryMemoryRepository()
	publisher := &recordingPublisher{}

	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-1",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeInject,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusCompleted,
		AnalysisStatus: domain.ProcessingStatusQueued,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "Please help me design the MemStation server architecture."},
			{Role: domain.RoleAssistant, Text: "We should start from the gateway and memory pipeline."},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}
	if err := memories.StoreDocument(ctx, domain.MemoryDocument{
		ID:             "summary:conv-1:v1",
		NodeID:         "node:summary:conv-1:v1",
		UserID:         "user-1",
		ConversationID: "conv-1",
		Type:           domain.MemoryTypeSummary,
		Content:        "User asked to design the MemStation server architecture and implementation roadmap.",
	}); err != nil {
		t.Fatalf("store summary: %v", err)
	}

	handler := handleAnalysisExtract(nilLogger(), Services{
		Conversations:  conversations,
		Memories:       memories,
		Publisher:      publisher,
		Analysis:       analysisservice.NewService(),
		EmbeddingModel: "test-embedding",
	})
	if err := handler(ctx, newTask(t, TypeAnalysisExtract, AnalysisPayload{
		UserID:         "user-1",
		ConversationID: "conv-1",
		SummaryIDs:     []string{"summary:conv-1:v1"},
	})); err != nil {
		t.Fatalf("handle analysis: %v", err)
	}

	facts, err := memories.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeFact}, 10)
	if err != nil {
		t.Fatalf("list facts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected one fact, got %d", len(facts))
	}
	impressions, err := memories.ListDocuments(ctx, "user-1", []domain.MemoryType{domain.MemoryTypeImpression}, 10)
	if err != nil {
		t.Fatalf("list impressions: %v", err)
	}
	if len(impressions) != 1 {
		t.Fatalf("expected one impression, got %d", len(impressions))
	}
	relations, err := memories.ListRelations(ctx, "user-1", "", []string{"node:summary:conv-1:v1"}, 2)
	if err != nil {
		t.Fatalf("list relations: %v", err)
	}
	if len(relations) < 2 {
		t.Fatalf("expected derived relations, got %d", len(relations))
	}
	conversation, err := conversations.GetConversation(ctx, "user-1", "conv-1")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if conversation.AnalysisStatus != domain.ProcessingStatusCompleted {
		t.Fatalf("expected completed analysis status, got %s", conversation.AnalysisStatus)
	}
	if len(publisher.tasks) != 3 {
		t.Fatalf("expected 3 follow-up tasks, got %d", len(publisher.tasks))
	}
}

func TestAnalysisExtractHandlerQueuesKeyMemoryForActiveNonCurrentGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := sql.NewInMemoryConversationRepository()
	memories := sql.NewInMemoryMemoryRepository()
	publisher := &recordingPublisher{}
	generations := memorygenservice.NewService(nil, "test-embedding")
	now := time.Now().UTC()

	if _, _, err := generations.CaptureSourceSnapshot(ctx, domain.ConversationRecord{
		ConversationID: "conv-active-generation",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeInject,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      now,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "Retained source state for active generation."},
		},
	}); err != nil {
		t.Fatalf("capture retained source: %v", err)
	}
	generation, sources, err := generations.CreateRebuildGeneration(ctx, memorygenservice.CreateRebuildInput{
		UserID:            "user-1",
		Scope:             "user",
		ActivateOnSuccess: false,
	})
	if err != nil {
		t.Fatalf("create rebuild generation: %v", err)
	}
	if _, ready, err := generations.MarkGenerationSourceCompleted(ctx, "user-1", generation.GenerationID, sources[0].SnapshotID, 0); err != nil {
		t.Fatalf("mark generation source completed: %v", err)
	} else if ready {
		t.Fatal("expected manual activation generation to remain pending activation")
	}
	generation, err = generations.ActivateGeneration(ctx, "user-1", generation.GenerationID)
	if err != nil {
		t.Fatalf("activate non-current generation: %v", err)
	}

	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-active-generation",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeInject,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusCompleted,
		AnalysisStatus: domain.ProcessingStatusQueued,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "Keep system key memory in sync after activation."},
			{Role: domain.RoleAssistant, Text: "The active generation should still reconcile recent conversation."},
		},
		Metadata: map[string]any{
			"memory_generation_id": generation.GenerationID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist active-generation conversation: %v", err)
	}
	if err := memories.StoreDocument(ctx, domain.MemoryDocument{
		ID:             generation.GenerationID + ":summary:conv-active-generation:v1",
		NodeID:         generation.GenerationID + ":node:summary:conv-active-generation:v1",
		GenerationID:   generation.GenerationID,
		UserID:         "user-1",
		ConversationID: "conv-active-generation",
		Type:           domain.MemoryTypeSummary,
		Content:        "Conversation summary for an active rebuilt generation.",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("store active-generation summary: %v", err)
	}

	handler := handleAnalysisExtract(nilLogger(), Services{
		Conversations:  conversations,
		Memories:       memories,
		Publisher:      publisher,
		Analysis:       analysisservice.NewService(),
		Generations:    generations,
		EmbeddingModel: "test-embedding",
	})
	if err := handler(ctx, newTask(t, TypeAnalysisExtract, AnalysisPayload{
		UserID:         "user-1",
		ConversationID: "conv-active-generation",
		SummaryIDs:     []string{generation.GenerationID + ":summary:conv-active-generation:v1"},
		GenerationID:   generation.GenerationID,
	})); err != nil {
		t.Fatalf("handle analysis for active rebuilt generation: %v", err)
	}

	foundKeyMemory := false
	for _, task := range publisher.tasks {
		if task.Type != TypeKeyMemoryReconcile {
			continue
		}
		foundKeyMemory = true
		if task.Payload["generation_id"] != generation.GenerationID {
			t.Fatalf("expected key memory follow-up to carry active generation id %s, got %+v", generation.GenerationID, task.Payload)
		}
	}
	if !foundKeyMemory {
		t.Fatalf("expected keymemory follow-up for active rebuilt generation, got %+v", publisher.tasks)
	}
}

func TestMemoryRebuildConversationSkipsSupersededSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	generations := memorygenservice.NewService(nil, "test-embedding")
	conversation := domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-rebuild-stale",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "initial retained snapshot"},
		},
	}

	first, _, err := generations.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("capture first snapshot: %v", err)
	}
	generation, _, err := generations.CreateRebuildGeneration(ctx, memorygenservice.CreateRebuildInput{
		UserID:            "user-1",
		Scope:             "conversation",
		ConversationID:    "conv-rebuild-stale",
		ActivateOnSuccess: false,
	})
	if err != nil {
		t.Fatalf("create rebuild generation: %v", err)
	}

	conversation.Messages = append(conversation.Messages, domain.UnifiedMessage{Role: domain.RoleAssistant, Text: "newer retained snapshot"})
	second, _, err := generations.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("capture second snapshot: %v", err)
	}
	if _, added, err := generations.AddSnapshotToGeneration(ctx, generation.GenerationID, second); err != nil {
		t.Fatalf("add second snapshot to generation: %v", err)
	} else if !added {
		t.Fatal("expected second snapshot to be queued into the candidate generation")
	}

	handler := handleMemoryRebuildConversation(nilLogger(), Services{
		Generations: generations,
	})
	if err := handler(ctx, newTask(t, TypeMemoryRebuildConversation, MemoryRebuildConversationPayload{
		UserID:       "user-1",
		GenerationID: generation.GenerationID,
		SnapshotID:   first.SnapshotID,
	})); err != nil {
		t.Fatalf("handle stale rebuild conversation: %v", err)
	}

	sources, err := generations.ListGenerationSources(ctx, "user-1", generation.GenerationID)
	if err != nil {
		t.Fatalf("list generation sources: %v", err)
	}
	statusBySnapshot := map[string]domain.MemoryGenerationSourceStatus{}
	for _, source := range sources {
		statusBySnapshot[source.SnapshotID] = source.Status
	}
	if statusBySnapshot[first.SnapshotID] != domain.MemoryGenerationSourceStatusCompleted {
		t.Fatalf("expected superseded snapshot to be marked completed, got %+v", statusBySnapshot)
	}
	if statusBySnapshot[second.SnapshotID] != domain.MemoryGenerationSourceStatusQueued {
		t.Fatalf("expected newest snapshot to remain queued, got %+v", statusBySnapshot)
	}
}

func TestAnalysisExtractHandlerSelectsSummaryDocumentByType(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := sql.NewInMemoryConversationRepository()
	memories := sql.NewInMemoryMemoryRepository()
	publisher := &recordingPublisher{}
	now := time.Now().UTC()

	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-typed-summary",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeInject,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusCompleted,
		AnalysisStatus: domain.ProcessingStatusQueued,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "Please capture the server rollout architecture plan."},
			{Role: domain.RoleAssistant, Text: "The summary should stay attached to the conversation summary node."},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}

	if err := memories.StoreDocument(ctx, domain.MemoryDocument{
		ID:             "summary:conv-typed-summary:v1",
		NodeID:         "node:summary:conv-typed-summary:v1",
		UserID:         "user-1",
		ConversationID: "conv-typed-summary",
		Type:           domain.MemoryTypeSummary,
		Content:        "User asked for a rollout architecture plan for the server.",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("store summary: %v", err)
	}
	if err := memories.StoreDocument(ctx, domain.MemoryDocument{
		ID:             "fact:conv-typed-summary:recent-topic",
		NodeID:         "node:fact:conv-typed-summary:recent-topic",
		UserID:         "user-1",
		ConversationID: "conv-typed-summary",
		Type:           domain.MemoryTypeFact,
		Content:        "Older derived fact that should not be reused as the summary input.",
		CreatedAt:      now.Add(time.Minute),
		UpdatedAt:      now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("store older fact: %v", err)
	}

	handler := handleAnalysisExtract(nilLogger(), Services{
		Conversations:  conversations,
		Memories:       memories,
		Publisher:      publisher,
		Analysis:       analysisservice.NewService(),
		EmbeddingModel: "test-embedding",
	})
	if err := handler(ctx, newTask(t, TypeAnalysisExtract, AnalysisPayload{
		UserID:         "user-1",
		ConversationID: "conv-typed-summary",
		SummaryIDs:     []string{"summary:conv-typed-summary:v1"},
	})); err != nil {
		t.Fatalf("handle analysis: %v", err)
	}

	relations, err := memories.ListRelations(ctx, "user-1", "", []string{"node:summary:conv-typed-summary:v1"}, 1)
	if err != nil {
		t.Fatalf("list summary relations: %v", err)
	}
	foundSummaryRelation := false
	for _, relation := range relations {
		if relation.SrcNodeID == "node:summary:conv-typed-summary:v1" && relation.RelationType == "derived_from" {
			foundSummaryRelation = true
			break
		}
	}
	if !foundSummaryRelation {
		t.Fatalf("expected derived relations to originate from the summary node, got %+v", relations)
	}
}

func TestConversationFinalizeHandlerSkipsMemoryPipelineWhenPersistDisabled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := sql.NewInMemoryConversationRepository()
	publisher := &recordingPublisher{}

	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-skip",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusActive,
		SummaryStatus:  domain.ProcessingStatusPending,
		AnalysisStatus: domain.ProcessingStatusPending,
		Metadata: map[string]any{
			"memory_persist_enabled": false,
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}

	handler := handleConversationFinalize(nilLogger(), Services{
		Conversations: conversations,
		Publisher:     publisher,
	})
	if err := handler(ctx, newTask(t, TypeConversationFinalize, map[string]any{
		"conversation_id": "conv-skip",
		"user_id":         "user-1",
		"finalize_reason": "explicit",
	})); err != nil {
		t.Fatalf("handle finalize skip: %v", err)
	}

	conversation, err := conversations.GetConversation(ctx, "user-1", "conv-skip")
	if err != nil {
		t.Fatalf("get skipped conversation: %v", err)
	}
	if conversation.SummaryStatus != domain.ProcessingStatusSkipped {
		t.Fatalf("expected skipped summary status, got %s", conversation.SummaryStatus)
	}
	if conversation.AnalysisStatus != domain.ProcessingStatusSkipped {
		t.Fatalf("expected skipped analysis status, got %s", conversation.AnalysisStatus)
	}
	if len(publisher.tasks) != 0 {
		t.Fatalf("expected no follow-up tasks when memory persist is disabled, got %d", len(publisher.tasks))
	}
}

func TestEmbeddingHandlersAndHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := vector.NewPGVectorStore(nil)

	summary := domain.MemoryDocument{
		ID:          "summary-1",
		NodeID:      "node-summary-1",
		UserID:      "user-1",
		Type:        domain.MemoryTypeSummary,
		Content:     "User and assistant discussed rollout ownership and release checks.",
		UpdatedAt:   time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
		Importance:  1.1,
		DecayFactor: 1,
	}
	fact := domain.MemoryDocument{
		ID:          "fact-1",
		NodeID:      "node-fact-1",
		UserID:      "user-1",
		Type:        domain.MemoryTypeFact,
		Content:     "Rollback owner is Alice Chen.",
		UpdatedAt:   time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
		Importance:  1,
		DecayFactor: 1,
	}
	if err := repo.StoreDocument(ctx, summary); err != nil {
		t.Fatalf("store summary: %v", err)
	}
	if err := repo.StoreDocument(ctx, fact); err != nil {
		t.Fatalf("store fact: %v", err)
	}

	embed := handleEmbeddingGenerate(nilLogger(), Services{Vectors: vectors})
	if err := embed(ctx, newTask(t, TypeEmbeddingGenerate, EmbeddingPayload{
		UserID:           "user-1",
		NodeID:           "node-summary-1",
		NodeType:         string(domain.MemoryTypeSummary),
		EmbeddingVersion: 1,
	})); err != nil {
		t.Fatalf("handle embedding generate: %v", err)
	}

	results, err := vectors.Search(ctx, domain.VectorSearchInput{
		UserID:           "user-1",
		Query:            "node-summary-1",
		TopK:             5,
		EmbeddingVersion: 1,
	})
	if err != nil {
		t.Fatalf("search vectors: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected embedded summary to be searchable")
	}

	mux := NewServeMux(nilLogger(), Services{
		Conversations: conversationsFromRecords(t, "user-1", "conv-helper"),
		Memories:      repo,
		Vectors:       vectors,
		Publisher:     &recordingPublisher{},
	})
	if mux == nil {
		t.Fatal("expected serve mux")
	}

	if got := buildSummary([]domain.UnifiedMessage{
		{Role: domain.RoleSystem, Text: "ignore"},
		{Role: domain.RoleUser, Text: "Need a release checklist."},
		{Role: domain.RoleAssistant, Text: "Let's capture the owner."},
	}); got == "" {
		t.Fatal("expected non-empty summary helper output")
	}
	if got := summarizeTopic("Release checklist ownership release checklist"); got == "" {
		t.Fatal("expected non-empty topic summary")
	}
	if len(extractKeywords("Release checklist ownership checklist rollback owner")) == 0 {
		t.Fatal("expected extracted keywords")
	}
	if got := truncate("abcdef", 4); got != "abcd..." {
		t.Fatalf("unexpected truncate result: %q", got)
	}
	if got := itoa(7); got != "7" {
		t.Fatalf("unexpected itoa result: %q", got)
	}
	if got := stringValue(map[string]any{"value": 1}, "value"); got != "1" {
		t.Fatalf("unexpected stringValue result: %q", got)
	}
	if got := finalizeStatusValue(map[string]any{"finalize_status": "timeout"}); got != domain.FinalizeStatusTimeout {
		t.Fatalf("unexpected finalize status: %s", got)
	}
	if !conversationMemoryPersistEnabled(domain.ConversationRecord{}) {
		t.Fatal("expected missing metadata to allow memory persistence")
	}
	if conversationMemoryPersistEnabled(domain.ConversationRecord{Metadata: map[string]any{"memory_persist_enabled": "false"}}) {
		t.Fatal("expected string false metadata to disable memory persistence")
	}
}

func TestEmbeddingGenerateUsesCurrentStoredDocumentContent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sql.NewInMemoryMemoryRepository()
	vectors := &recordingVectorStore{}
	generationID := memorygenservice.CurrentGenerationID("user-1")

	if err := repo.StoreDocument(ctx, domain.MemoryDocument{
		ID:           generationID + ":summary:conv-embed:v1",
		NodeID:       generationID + ":node:summary:conv-embed:v1",
		GenerationID: generationID,
		UserID:       "user-1",
		Type:         domain.MemoryTypeSummary,
		Content:      "Fresh stored summary content should win over stale payload text.",
		Metadata: map[string]any{
			"topic_label": "fresh summary",
			"keywords":    []string{"fresh", "summary"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("store current document: %v", err)
	}

	handler := handleEmbeddingGenerate(nilLogger(), Services{
		Memories: repo,
		Vectors:  vectors,
	})
	if err := handler(ctx, newTask(t, TypeEmbeddingGenerate, EmbeddingPayload{
		UserID:           "user-1",
		GenerationID:     generationID,
		NodeID:           generationID + ":node:summary:conv-embed:v1",
		NodeType:         string(domain.MemoryTypeSummary),
		EmbeddingVersion: 1,
		Content:          "Stale payload content should not be embedded.",
		EmbeddingModel:   "test-embedding",
	})); err != nil {
		t.Fatalf("handle embedding generate: %v", err)
	}

	if !strings.Contains(vectors.lastUpsert.Content, "Fresh stored summary content should win") {
		t.Fatalf("expected embedding content to come from the stored document, got %q", vectors.lastUpsert.Content)
	}
	if strings.Contains(vectors.lastUpsert.Content, "Stale payload content should not be embedded.") {
		t.Fatalf("expected stale payload content to be ignored, got %q", vectors.lastUpsert.Content)
	}
}

func TestMarkConversationFailedAndRecordAuditHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := sql.NewInMemoryConversationRepository()
	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-fail",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeInject,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusRunning,
		AnalysisStatus: domain.ProcessingStatusRunning,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}

	markConversationFailed(ctx, Services{Conversations: conversations}, "user-1", "conv-fail", true)
	conversation, err := conversations.GetConversation(ctx, "user-1", "conv-fail")
	if err != nil {
		t.Fatalf("get failed conversation: %v", err)
	}
	if conversation.SummaryStatus != domain.ProcessingStatusFailed {
		t.Fatalf("expected failed summary status, got %s", conversation.SummaryStatus)
	}

	var recorded []domain.AuditEvent
	recordAudit(ctx, Services{
		RecordAudit: func(_ context.Context, event domain.AuditEvent) {
			recorded = append(recorded, event)
		},
	}, domain.AuditEvent{Action: "test"})
	if len(recorded) != 1 || recorded[0].Action != "test" {
		t.Fatalf("expected recorded audit event, got %+v", recorded)
	}
}

func conversationsFromRecords(t *testing.T, userID, conversationID string) *sql.InMemoryConversationRepository {
	t.Helper()
	repo := sql.NewInMemoryConversationRepository()
	if err := repo.PersistExchange(context.Background(), domain.ConversationRecord{
		ConversationID: conversationID,
		UserID:         userID,
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusActive,
		SummaryStatus:  domain.ProcessingStatusPending,
		AnalysisStatus: domain.ProcessingStatusPending,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed conversation repo: %v", err)
	}
	return repo
}

func newTask(t *testing.T, taskType string, payload any) *asynq.Task {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return asynq.NewTask(taskType, raw)
}

func nilLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
