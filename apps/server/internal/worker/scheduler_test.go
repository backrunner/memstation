package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"

	conversationservice "memstation/apps/server/internal/service/conversation"
)

func TestSchedulerRunStopsOnContextCancelAndTracksTimeoutWindow(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	relay := NewRelay(logger, conversationstore.NewInMemoryOutboxRepository(), NewPublisher(logger, nil))
	conversations := conversationservice.NewService(conversationstore.NewInMemoryConversationRepository(), conversationstore.NewInMemoryOutboxRepository(), 4, 256)
	scheduler := NewScheduler(logger, relay, conversations, 5*time.Minute)

	if got := scheduler.currentTimeoutWindow(); got != 5*time.Minute {
		t.Fatalf("unexpected initial timeout window: %s", got)
	}
	scheduler.UpdateTimeoutWindow(2 * time.Minute)
	if got := scheduler.currentTimeoutWindow(); got != 2*time.Minute {
		t.Fatalf("unexpected updated timeout window: %s", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := scheduler.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
