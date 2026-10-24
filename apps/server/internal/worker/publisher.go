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
	logger *slog.Logger
	client *asynq.Client
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
	task, err := prepareScopedTask(ctx, task)
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
