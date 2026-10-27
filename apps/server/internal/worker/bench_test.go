package worker

import (
	"context"
	"fmt"
	"testing"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"

	"memstation/apps/server/internal/domain"
)

// noopPublisher is a TaskPublisher stand-in so the relay bench isolates
// outbox scan + mark-processed from any queue backend latency.
type noopPublisher struct{ count int }

func (n *noopPublisher) Publish(context.Context, domain.TaskMessage) error {
	n.count++
	return nil
}

// BenchmarkOutboxRelayFlush measures the outbox relay throughput when
// draining a batch of pending events. Operational monitoring warns about
// `outbox.pending` growth; this bench keeps the drain path cheap.
func BenchmarkOutboxRelayFlush(b *testing.B) {
	logger := nilLogger()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		outbox := conversationstore.NewInMemoryOutboxRepository()
		for j := 0; j < 50; j++ {
			_ = outbox.Enqueue(context.Background(), domain.OutboxEvent{
				ID:          fmt.Sprintf("conversation.finalize:conv-%d-%d", i, j),
				UserID:      "bench-user",
				EventType:   TypeConversationFinalize,
				AggregateID: fmt.Sprintf("conv-%d-%d", i, j),
				Payload:     map[string]any{"conversation_id": fmt.Sprintf("conv-%d-%d", i, j)},
			})
		}
		relay := fixtureRelay(logger, outbox, &noopPublisher{})
		b.StartTimer()
		if err := relay.Flush(context.Background()); err != nil {
			b.Fatalf("flush: %v", err)
		}
	}
}

// BenchmarkOutboxEnqueue measures the in-memory outbox write path used by the
// finalize pipeline on every conversation termination.
func BenchmarkOutboxEnqueue(b *testing.B) {
	outbox := conversationstore.NewInMemoryOutboxRepository()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := outbox.Enqueue(context.Background(), domain.OutboxEvent{
			ID:          fmt.Sprintf("conversation.finalize:conv-%d", i),
			UserID:      "bench-user",
			EventType:   TypeConversationFinalize,
			AggregateID: fmt.Sprintf("conv-%d", i),
			Payload:     map[string]any{"conversation_id": fmt.Sprintf("conv-%d", i)},
		}); err != nil {
			b.Fatalf("enqueue: %v", err)
		}
	}
}
