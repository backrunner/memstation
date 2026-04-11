package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"memstation/apps/server/internal/domain"
	adminservice "memstation/apps/server/internal/service/admin"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	sqlstore "memstation/apps/server/internal/store/sql"
)

type recordingTaskPublisher struct {
	tasks []domain.TaskMessage
}

func (p *recordingTaskPublisher) Publish(_ context.Context, task domain.TaskMessage) error {
	p.tasks = append(p.tasks, task)
	return nil
}

func TestAdminConversationDetailHandlerRespectsGenerationQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := sqlstore.NewInMemoryConversationRepository()
	media := sqlstore.NewInMemoryMediaRepository()
	memories := sqlstore.NewInMemoryMemoryRepository()
	service := adminservice.NewService(conversations, media, memories, nil, nil, nil)
	userID := "user-1"
	conversationID := "conv-1"
	currentGenerationID := memorygenservice.CurrentGenerationID(userID)
	candidateGenerationID := "gen:rebuild:user-1"
	now := time.Date(2026, 4, 9, 9, 0, 0, 0, time.UTC)

	if err := conversations.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: conversationID,
		UserID:         userID,
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		SummaryStatus:  domain.ProcessingStatusCompleted,
		AnalysisStatus: domain.ProcessingStatusCompleted,
		Metadata: map[string]any{
			"memory_generation_id": currentGenerationID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist conversation: %v", err)
	}
	if err := conversations.ReplaceSegments(ctx, userID, conversationID, []domain.ConversationSegment{
		{
			ID:             "segment:conv-1:000",
			GenerationID:   currentGenerationID,
			UserID:         userID,
			ConversationID: conversationID,
			SegmentIndex:   0,
			Content:        "current generation segment",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("replace current segments: %v", err)
	}
	if err := conversations.ReplaceSegments(ctx, userID, conversationID, []domain.ConversationSegment{
		{
			ID:             candidateGenerationID + ":segment:conv-1:000",
			GenerationID:   candidateGenerationID,
			UserID:         userID,
			ConversationID: conversationID,
			SegmentIndex:   0,
			Content:        "candidate generation segment",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("replace candidate segments: %v", err)
	}

	request := requestWithURLParams(
		http.MethodGet,
		"/api/admin/conversations/conv-1?user_id=user-1&generation_id="+candidateGenerationID,
		nil,
		map[string]string{"id": conversationID},
	)
	recorder := httptest.NewRecorder()
	adminConversationDetailHandler(service).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload domain.ConversationRecord
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Segments) != 1 || payload.Segments[0].GenerationID != candidateGenerationID {
		t.Fatalf("expected candidate-generation segments, got %+v", payload.Segments)
	}
	if got := payload.Metadata["memory_generation_id"]; got != candidateGenerationID {
		t.Fatalf("expected response metadata to reflect requested generation %s, got %v", candidateGenerationID, got)
	}
}

func TestAdminConversationMemoryHandlerReturnsResolvedGenerationID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := sqlstore.NewInMemoryMemoryRepository()
	generations := memorygenservice.NewService(nil, "test-embedding")
	activeGeneration, err := generations.GetActiveGeneration(ctx, "user-1")
	if err != nil {
		t.Fatalf("ensure active generation: %v", err)
	}
	service := memoryservice.NewService(nil, repo)
	service.SetGenerationService(generations)

	if err := repo.StoreDocument(ctx, domain.MemoryDocument{
		ID:             activeGeneration.GenerationID + ":summary:conv-1:v1",
		NodeID:         activeGeneration.GenerationID + ":node:summary:conv-1:v1",
		GenerationID:   activeGeneration.GenerationID,
		UserID:         "user-1",
		ConversationID: "conv-1",
		Type:           domain.MemoryTypeSummary,
		Content:        "summary content",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("store generation-scoped summary: %v", err)
	}

	request := requestWithURLParams(
		http.MethodGet,
		"/api/admin/conversations/conv-1/memory?user_id=user-1",
		nil,
		map[string]string{"id": "conv-1"},
	)
	recorder := httptest.NewRecorder()
	adminConversationMemoryHandler(nil, service, generations).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		GenerationID string `json:"generation_id"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.GenerationID != activeGeneration.GenerationID {
		t.Fatalf("expected resolved generation id %s, got %s", activeGeneration.GenerationID, payload.GenerationID)
	}
}

func TestAdminMemoryRebuildCreateHandlerRequiresPublisher(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := memorygenservice.NewService(nil, "test-embedding")
	if _, _, err := service.CaptureSourceSnapshot(ctx, domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "retained snapshot for rebuild"},
		},
	}); err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}

	body := bytes.NewBufferString(`{"user_id":"user-1","scope":"conversation","conversation_id":"conv-1","activate_on_success":true}`)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/memory/rebuilds", body)
	recorder := httptest.NewRecorder()
	adminMemoryRebuildCreateHandler(service, nil, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 when publisher is missing, got %d: %s", recorder.Code, recorder.Body.String())
	}
	generations, err := service.ListGenerations(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list generations: %v", err)
	}
	if len(generations) != 0 {
		t.Fatalf("expected no generation to be created when publisher is missing, got %+v", generations)
	}
}

func TestAdminMemoryRebuildCreateHandlerRejectsConversationAutoActivation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := memorygenservice.NewService(nil, "test-embedding")
	publisher := &recordingTaskPublisher{}
	if _, _, err := service.CaptureSourceSnapshot(ctx, domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "retained snapshot for conversation-only rebuild"},
		},
	}); err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}

	body := bytes.NewBufferString(`{"user_id":"user-1","scope":"conversation","conversation_id":"conv-1","activate_on_success":true}`)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/memory/rebuilds", body)
	recorder := httptest.NewRecorder()
	adminMemoryRebuildCreateHandler(service, publisher, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if len(publisher.tasks) != 0 {
		t.Fatalf("expected no rebuild tasks to be published for invalid request, got %+v", publisher.tasks)
	}
	generations, err := service.ListGenerations(ctx, "user-1", 10)
	if err != nil {
		t.Fatalf("list generations: %v", err)
	}
	if len(generations) != 0 {
		t.Fatalf("expected no generation to be created for invalid request, got %+v", generations)
	}
}

func requestWithURLParams(method, target string, body *bytes.Buffer, params map[string]string) *http.Request {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body.Bytes())
	}
	request := httptest.NewRequest(method, target, reader)
	routeCtx := chi.NewRouteContext()
	for key, value := range params {
		routeCtx.URLParams.Add(key, value)
	}
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx))
}
