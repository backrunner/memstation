package worker

import (
	"context"
	"errors"
	"log/slog"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

type Relay struct {
	logger    *slog.Logger
	outbox    ports.OutboxRepository
	publisher ports.TaskPublisher
}

func NewRelay(logger *slog.Logger, outbox ports.OutboxRepository, publisher ports.TaskPublisher) *Relay {
	return &Relay{logger: logger, outbox: outbox, publisher: publisher}
}

func (r *Relay) Flush(ctx context.Context) error {
	var publishErrs []error
	for {
		events, err := r.outbox.Pending(ctx, 100)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			break
		}
		batchHadFailure := false
		for _, event := range events {
			task := mapOutboxToTask(event)
			if err := r.publisher.Publish(ctx, task); err != nil {
				r.logger.ErrorContext(ctx, "outbox publish failed", "event_id", event.ID, "error", err)
				publishErrs = append(publishErrs, err)
				batchHadFailure = true
				continue
			}
			if err := r.outbox.MarkProcessed(ctx, event.ID); err != nil {
				return err
			}
		}
		if batchHadFailure {
			break
		}
	}
	return errors.Join(publishErrs...)
}

func mapOutboxToTask(event domain.OutboxEvent) domain.TaskMessage {
	queue := QueueDefault
	if event.EventType == TypeConversationFinalize {
		queue = QueueCritical
	}
	return domain.TaskMessage{
		Type:      event.EventType,
		Queue:     queue,
		UniqueKey: event.EventType + ":" + event.AggregateID,
		Payload:   event.Payload,
	}
}
