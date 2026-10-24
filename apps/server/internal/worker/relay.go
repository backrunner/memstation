package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
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
			var envelope taskScope
			raw, marshalErr := json.Marshal(event.Payload)
			if marshalErr != nil || json.Unmarshal(raw, &envelope) != nil || envelope.Version < 0 || envelope.Version > 1 || envelope.Version == 1 && envelope.WorkspaceID == "" {
				publishErrs = append(publishErrs, domain.ErrAccessDenied)
				batchHadFailure = true
				continue
			}
			user, _ := event.Payload["user_id"].(string)
			workspace, _ := event.Payload["workspace_id"].(string)
			bound, scopeErr := security.WithPersonalDataScope(ctx, event.UserID, workspace)
			if scopeErr != nil || user != event.UserID {
				publishErrs = append(publishErrs, domain.ErrAccessDenied)
				batchHadFailure = true
				continue
			}
			task := mapOutboxToTask(event)
			if err := r.publisher.Publish(bound, task); err != nil {
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
	uniqueID := event.AggregateID
	if event.EventType == TypeEmbeddingGenerate || event.EventType == TypeEmbeddingDelete || event.EventType == TypeKeyMemoryGraphSync {
		uniqueID = event.ID
	}
	return domain.TaskMessage{
		Type:      event.EventType,
		Queue:     queue,
		UniqueKey: event.EventType + ":" + uniqueID,
		Payload:   event.Payload,
	}
}
