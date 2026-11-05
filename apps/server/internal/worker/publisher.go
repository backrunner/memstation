package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"

	"memstation/apps/server/internal/domain"
)

var ErrQueueUnavailable = errors.New("queue runtime is not configured")

type Publisher struct {
	processingPolicyID string
	logger             *slog.Logger
	client             *asynq.Client
}

func NewPublisher(logger *slog.Logger, client *asynq.Client) *Publisher {
	return &Publisher{logger: logger, client: client}
}

func (p *Publisher) Publish(ctx context.Context, task domain.TaskMessage) error {
	if p.client == nil {
		if p.logger != nil {
			p.logger.WarnContext(ctx, "task publish failed; asynq client not configured", "task_type", task.Type, "queue", task.Queue)
		}
		return ErrQueueUnavailable
	}
	var err error
	task, err = BindProcessingPolicy(task, p.processingPolicyID)
	if err != nil {
		return err
	}
	task, err = prepareScopedTask(ctx, task)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(task.Payload)
	if err != nil {
		return err
	}
	options := []asynq.Option{asynq.Queue(task.Queue)}
	if task.UniqueKey != "" {
		options = append(options, asynq.TaskID(task.UniqueKey))
	}
	if _, err := p.client.EnqueueContext(ctx, asynq.NewTask(task.Type, payload), options...); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			return nil
		}
		return fmt.Errorf("enqueue task: %w", err)
	}
	return nil
}

// BindProcessingPolicy is shared by network publishers and durable evaluation
// queues. It never mutates the caller's payload or relaxes consumer authority.
func BindProcessingPolicy(task domain.TaskMessage, policyID string) (domain.TaskMessage, error) {
	if policyID != "" && processingTaskType(task.Type) {
		payload := make(map[string]any, len(task.Payload)+1)
		for key, value := range task.Payload {
			payload[key] = value
		}
		if policy, exists := payload["processing_policy_id"]; exists && policy != policyID {
			return task, errors.New("task processing policy mismatch")
		}
		payload["processing_policy_id"] = policyID
		task.Payload = payload
		if task.UniqueKey != "" {
			task.UniqueKey += ":" + policyID
		}
	}
	return task, nil
}

func (p *Publisher) SetProcessingPolicy(id string) { p.processingPolicyID = id }
func processingTaskType(kind string) bool {
	switch kind {
	case TypeSourceIndex, TypeMediaExtract, TypeSummaryGenerate, TypeAnalysisExtract, TypeKeyMemoryReconcile, TypeMemoryRebuildConversation:
		return true
	}
	return false
}
