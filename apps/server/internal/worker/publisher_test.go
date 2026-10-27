package worker

import (
	"context"
	"errors"
	"testing"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
)

func TestRelayRetryAfterEnqueueKeepsOneScopedTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	redis := miniredis.RunT(t)
	options := asynq.RedisClientOpt{Addr: redis.Addr()}
	client := asynq.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	inspector := asynq.NewInspector(options)
	t.Cleanup(func() { _ = inspector.Close() })
	publisher := NewPublisher(nilLogger(), client)
	outbox := conversationstore.NewInMemoryOutboxRepository()
	event := domain.OutboxEvent{
		ID: "conversation.finalize:retry", UserID: "owner", EventType: TypeConversationFinalize, AggregateID: "retry",
		Payload: map[string]any{"schema_version": 2, "workspace_id": domain.PersonalWorkspaceID("owner"), "user_id": "owner", "conversation_id": "retry"},
	}
	if err := outbox.Enqueue(ctx, event); err != nil {
		t.Fatal(err)
	}
	// Simulate a relay crash after Redis accepted the task but before the SQL
	// event was marked processed. The next relay must accept the same task ID.
	if err := publisher.Publish(ctx, mapOutboxToTask(event)); err != nil {
		t.Fatal(err)
	}
	if err := fixtureRelay(nilLogger(), outbox, publisher).Flush(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := outbox.Pending(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("relay did not complete replay: %v, %d pending", err, len(pending))
	}
	info, err := inspector.GetQueueInfo(QueueCritical)
	if err != nil || info.Pending != 1 {
		t.Fatalf("replay duplicated the queued task: %+v, %v", info, err)
	}
	// Identical external IDs in a different owner's scope are distinct jobs.
	event.UserID = "other-owner"
	event.Payload = map[string]any{"user_id": event.UserID, "conversation_id": "retry"}
	if err := publisher.Publish(ctx, mapOutboxToTask(event)); err != nil {
		t.Fatal(err)
	}
	info, err = inspector.GetQueueInfo(QueueCritical)
	if err != nil || info.Pending != 2 {
		t.Fatalf("scope collision suppressed a different owner's task: %+v, %v", info, err)
	}
}

func TestPublisherReturnsUnavailableWhenQueueIsDisabled(t *testing.T) {
	t.Parallel()

	publisher := NewPublisher(nilLogger(), nil)
	err := publisher.Publish(context.Background(), domain.TaskMessage{
		Type:  TypeAnalysisExtract,
		Queue: QueueLow,
	})
	if !errors.Is(err, ErrQueueUnavailable) {
		t.Fatalf("expected ErrQueueUnavailable, got %v", err)
	}
}

func TestRelayKeepsOutboxPendingWhenPublishFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	outbox := conversationstore.NewInMemoryOutboxRepository()
	if err := outbox.Enqueue(ctx, domain.OutboxEvent{
		ID:          "conversation.finalize:conv-1",
		UserID:      "user-1",
		EventType:   TypeConversationFinalize,
		AggregateID: "conv-1",
		Payload: map[string]any{
			"schema_version": 2, "workspace_id": domain.PersonalWorkspaceID("user-1"), "user_id": "user-1",
			"conversation_id": "conv-1",
		},
	}); err != nil {
		t.Fatalf("enqueue outbox event: %v", err)
	}

	relay := fixtureRelay(nilLogger(), outbox, NewPublisher(nilLogger(), nil))
	if err := relay.Flush(ctx); !errors.Is(err, ErrQueueUnavailable) {
		t.Fatalf("expected ErrQueueUnavailable from relay flush, got %v", err)
	}

	pending, err := outbox.Pending(ctx, 10)
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected outbox event to remain pending, got %d", len(pending))
	}
	if pending[0].ProcessedAt != nil {
		t.Fatal("expected outbox event to remain unprocessed")
	}
}

func TestMemoryMutationOutboxUsesEventIdentityForTaskDeduplication(t *testing.T) {
	t.Parallel()
	event := domain.OutboxEvent{
		ID:          "embedding.generate:node-1:42",
		EventType:   TypeEmbeddingGenerate,
		AggregateID: "node-1",
		Payload:     map[string]any{"embedding_revision": 42},
	}
	task := mapOutboxToTask(event)
	if task.UniqueKey != TypeEmbeddingGenerate+":"+event.ID {
		t.Fatalf("expected revision-specific task identity, got %q", task.UniqueKey)
	}
}
