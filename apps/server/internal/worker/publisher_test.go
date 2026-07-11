package worker

import (
	"context"
	"errors"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/store/sql"
)

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
	outbox := sql.NewInMemoryOutboxRepository()
	if err := outbox.Enqueue(ctx, domain.OutboxEvent{
		ID:          "conversation.finalize:conv-1",
		UserID:      "user-1",
		EventType:   TypeConversationFinalize,
		AggregateID: "conv-1",
		Payload: map[string]any{
			"user_id":         "user-1",
			"conversation_id": "conv-1",
		},
	}); err != nil {
		t.Fatalf("enqueue outbox event: %v", err)
	}

	relay := NewRelay(nilLogger(), outbox, NewPublisher(nilLogger(), nil))
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
