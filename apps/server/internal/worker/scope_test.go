package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	sqlstore "memstation/apps/server/internal/store/sql"
)

func TestTaskScopeValidationPrecedesSideEffects(t *testing.T) {
	ctx := context.Background()
	services := Services{Conversations: conversationsFromRecords(t, "owner", "conversation")}
	base := map[string]any{"schema_version": 1, "user_id": "owner", "workspace_id": domain.PersonalWorkspaceID("owner"), "conversation_id": "conversation", "agent_id": domain.LegacyAgentID("owner")}
	bound, err := bindTaskScope(ctx, newTask(t, TypeConversationFinalize, base), services)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = security.PersonalScope(bound, "another", ""); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("worker context allowed another owner")
	}
	for _, change := range []map[string]any{
		{"workspace_id": "another"}, {"user_id": "another"}, {"agent_id": "another"}, {"schema_version": 2}, {"schema_version": -1}, {"schema_version": 1, "workspace_id": ""}, {"workspace_id": 123}, {"user_id": ""}, {"conversation_id": "missing"},
	} {
		payload := cloneMap(base)
		for k, v := range change {
			payload[k] = v
		}
		if _, err = bindTaskScope(ctx, newTask(t, TypeConversationFinalize, payload), services); !errors.Is(err, asynq.SkipRetry) {
			t.Fatalf("invalid scope can retry/work: %v %v", change, err)
		}
	}
	legacy := map[string]any{"user_id": "owner", "conversation_id": "conversation"}
	if _, err = bindTaskScope(ctx, newTask(t, TypeConversationFinalize, legacy), services); err != nil {
		t.Fatalf("legacy Personal job: %v", err)
	}
	// Every registered handler rejects an unsupported workspace before touching
	// nil dependencies or invoking external analysis, embedding or media services.
	mux := NewServeMux(nilLogger(), Services{})
	for _, kind := range []string{TypeConversationFinalize, TypeMediaExtract, TypeSummaryGenerate, TypeAnalysisExtract, TypeEmbeddingGenerate, TypeEmbeddingDelete, TypeKeyMemoryGraphSync, TypeKeyMemoryReconcile, TypeMemoryRebuildConversation, TypeMemoryRebuildActivate} {
		err = mux.ProcessTask(ctx, newTask(t, kind, map[string]any{"user_id": "owner", "workspace_id": "other", "schema_version": 1}))
		if !errors.Is(err, asynq.SkipRetry) {
			t.Fatalf("%s bypassed scope guard: %v", kind, err)
		}
	}
}

func TestTaskPublicationPreservesScopeAndStableIdentity(t *testing.T) {
	ctx, err := security.WithPersonalDataScope(context.Background(), "owner", domain.PersonalWorkspaceID("owner"))
	if err != nil {
		t.Fatal(err)
	}
	task := domain.TaskMessage{Type: TypeSummaryGenerate, UniqueKey: "same-external-id", Payload: map[string]any{"user_id": "owner", "conversation_id": "conversation"}}
	prepared, err := prepareScopedTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := prepareScopedTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.UniqueKey != retry.UniqueKey || prepared.Payload["schema_version"] != 1 || prepared.Payload["workspace_id"] != domain.PersonalWorkspaceID("owner") {
		t.Fatal("unstable/unscoped task")
	}
	if _, ok := task.Payload["workspace_id"]; ok {
		t.Fatal("publisher mutated caller payload")
	}
	task.Payload = map[string]any{"user_id": "another"}
	if _, err = prepareScopedTask(ctx, task); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("worker published work for another owner")
	}
	other, err := prepareScopedTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if other.UniqueKey == prepared.UniqueKey {
		t.Fatal("different owners share a queue task ID")
	}
	task.Payload = map[string]any{"user_id": "owner", "schema_version": 2}
	if _, err = prepareScopedTask(ctx, task); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("unsupported task schema downgraded")
	}
}

func TestRelayRejectsPayloadOwnerSubstitution(t *testing.T) {
	ctx := context.Background()
	outbox := sqlstore.NewInMemoryOutboxRepository()
	publisher := &recordingPublisher{}
	if err := outbox.Enqueue(ctx, domain.OutboxEvent{ID: "bad-owner", UserID: "owner", EventType: TypeEmbeddingDelete, Payload: map[string]any{"user_id": "another", "node_id": "node"}}); err != nil {
		t.Fatal(err)
	}
	if err := NewRelay(nilLogger(), outbox, publisher).Flush(ctx); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatalf("relay scope substitution: %v", err)
	}
	if len(publisher.tasks) != 0 {
		t.Fatal("invalid outbox published")
	}
	pending, err := outbox.Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatal("invalid outbox silently consumed")
	}
}

type failFirstPublisher struct {
	calls int
	tasks []domain.TaskMessage
}

func (p *failFirstPublisher) Publish(_ context.Context, task domain.TaskMessage) error {
	p.calls++
	if p.calls == 1 {
		return ErrQueueUnavailable
	}
	p.tasks = append(p.tasks, task)
	return nil
}

func TestFinalizeRetryPublishesQueuedSummary(t *testing.T) {
	ctx := context.Background()
	repo := conversationsFromRecords(t, "owner", "conversation")
	publisher := &failFirstPublisher{}
	handler := handleConversationFinalize(nilLogger(), Services{Conversations: repo, Publisher: publisher})
	task := newTask(t, TypeConversationFinalize, map[string]any{"user_id": "owner", "conversation_id": "conversation", "finalize_reason": "explicit", "finalize_status": "ended"})
	if err := handler(ctx, task); !errors.Is(err, ErrQueueUnavailable) {
		t.Fatalf("first publish: %v", err)
	}
	if err := handler(ctx, task); err != nil {
		t.Fatal(err)
	}
	if len(publisher.tasks) != 1 || publisher.tasks[0].Type != TypeSummaryGenerate {
		t.Fatal("queued state swallowed retry after publish failure")
	}
}
