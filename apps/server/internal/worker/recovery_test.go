package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	sqlstore "memstation/apps/server/internal/store/sql"
)

func TestRunRecoveryDrillCompletesBacklog(t *testing.T) {
	t.Parallel()

	report, err := RunRecoveryDrill(context.Background(), RecoveryOptions{
		Conversations:     24,
		WorkerConcurrency: 6,
		MessagesPerTurn:   3,
		Timeout:           10 * time.Second,
	})
	if err != nil {
		t.Fatalf("run recovery drill: %v", err)
	}
	if err := ValidateRecoveryReport(report, 5*time.Second); err != nil {
		t.Fatalf("validate recovery report: %v", err)
	}
	if report.PeakBacklog <= 0 {
		t.Fatalf("expected a positive peak backlog, got %d", report.PeakBacklog)
	}
	if report.TotalTasks < report.Conversations*8 {
		t.Fatalf("expected at least %d processed tasks, got %d", report.Conversations*8, report.TotalTasks)
	}
	expectedCounts := map[string]int{
		TypeConversationFinalize: report.Conversations,
		TypeSummaryGenerate:      report.Conversations,
		TypeEmbeddingGenerate:    report.Conversations*3 + report.FactDocuments + report.GenericDocuments,
		TypeAnalysisExtract:      report.Conversations,
		TypeKeyMemoryReconcile:   report.Conversations,
	}
	for taskType, expected := range expectedCounts {
		if report.ProcessedByType[taskType] != expected {
			t.Fatalf("expected %s count %d, got %d", taskType, expected, report.ProcessedByType[taskType])
		}
	}
}

func TestRunRecoveryDrillDrainsGrowingFanout(t *testing.T) {
	for _, tc := range []struct {
		name          string
		conversations int
		queueBuffer   int
	}{
		{name: "nightly", conversations: 256},
		{name: "small initial queue", conversations: 16, queueBuffer: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := RunRecoveryDrill(context.Background(), RecoveryOptions{
				Conversations:     tc.conversations,
				WorkerConcurrency: 12,
				MessagesPerTurn:   4,
				QueueBuffer:       tc.queueBuffer,
				Timeout:           45 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateRecoveryReport(report, 0); err != nil {
				t.Fatal(err)
			}
			for taskType, enqueued := range report.EnqueuedByType {
				if processed := report.ProcessedByType[taskType]; processed != enqueued {
					t.Fatalf("%s lost tasks: enqueued %d, processed %d", taskType, enqueued, processed)
				}
			}
		})
	}
}

func TestRecoveryQueueHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := newRecoveryQueue(1)
	cancel()
	if err := queue.Publish(ctx, domain.TaskMessage{Type: TypeSummaryGenerate}); err == nil {
		t.Fatal("accepted work after cancellation")
	}
	if _, available := queue.next(ctx); available {
		t.Fatal("returned work after cancellation")
	}
}

func TestRelayFlushProcessesMultiplePendingBatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	outbox := sqlstore.NewInMemoryOutboxRepository()
	publisher := &recordingPublisher{}
	relay := NewRelay(slog.New(slog.NewTextHandler(io.Discard, nil)), outbox, publisher)

	for idx := 0; idx < 135; idx++ {
		if err := outbox.Enqueue(ctx, domain.OutboxEvent{
			ID:            fmt.Sprintf("event-%03d", idx),
			UserID:        "user-1",
			EventType:     TypeConversationFinalize,
			AggregateType: "conversation",
			AggregateID:   fmt.Sprintf("conv-%03d", idx),
			Payload: map[string]any{
				"conversation_id": fmt.Sprintf("conv-%03d", idx),
				"user_id":         "user-1",
			},
			Status:      "pending",
			AvailableAt: time.Now().UTC(),
			CreatedAt:   time.Now().UTC(),
		}); err != nil {
			t.Fatalf("enqueue outbox: %v", err)
		}
	}

	if err := relay.Flush(ctx); err != nil {
		t.Fatalf("flush relay: %v", err)
	}
	pending, err := outbox.Pending(ctx, 200)
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected drained outbox, got %d pending events", len(pending))
	}
	if len(publisher.tasks) != 135 {
		t.Fatalf("expected 135 published tasks, got %d", len(publisher.tasks))
	}
}
