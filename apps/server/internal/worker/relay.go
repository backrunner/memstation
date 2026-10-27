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
	authority ports.MemoryTaskAuthority
}

func NewRelay(logger *slog.Logger, outbox ports.OutboxRepository, publisher ports.TaskPublisher) *Relay {
	return &Relay{logger: logger, outbox: outbox, publisher: publisher}
}

func (r *Relay) SetTaskAuthority(a ports.MemoryTaskAuthority) { r.authority = a }

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
			if marshalErr != nil || json.Unmarshal(raw, &envelope) != nil || envelope.Version != 2 || envelope.WorkspaceID == "" {
				publishErrs = append(publishErrs, domain.ErrAccessDenied)
				batchHadFailure = true
				continue
			}
			user, _ := event.Payload["user_id"].(string)
			workspace, _ := event.Payload["workspace_id"].(string)
			var bound context.Context
			var scopeErr error
			if r.authority == nil {
				scopeErr = domain.ErrAccessDenied
			} else {
				scopeErr = r.authority.ValidateMemoryTask(ctx, domain.MemoryTaskScope{ReceiptID: envelope.ReceiptID, OperationID: envelope.OperationID, UserID: user, WorkspaceID: workspace, PartitionID: envelope.PartitionID, ConversationID: envelope.ConversationID, NodeID: envelope.NodeID, GenerationID: envelope.GenerationID, ConversationRevision: envelope.ConversationRevision})
				if scopeErr == nil {
					bound, scopeErr = security.WithDataScope(ctx, user, workspace, envelope.PartitionID)
				}
			}
			if errors.Is(scopeErr, domain.ErrMemoryTaskObsolete) && user == event.UserID {
				// Persistent authority proved this exact task obsolete. Keeping it
				// pending would starve valid outbox work behind a revoked source.
				if err := r.outbox.MarkProcessed(ctx, event.ID); err != nil {
					return err
				}
				continue
			}
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
	if event.EventType == TypeMemoryRebuildConversation {
		queue = QueueBulk
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
