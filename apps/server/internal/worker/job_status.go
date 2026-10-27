package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"memstation/apps/server/internal/aicall"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"

	"github.com/hibiken/asynq"
)

// Retry count passed by asynq is the count before this failure. Keeping this
// deterministic makes the receipt's next retry time match the queue schedule.
func MemoryRetryDelay(attempt int, _ error, _ *asynq.Task) time.Duration {
	return min(5*time.Second*time.Duration(1<<min(max(attempt, 0), 10)), time.Hour)
}

func trackMemoryJob(services Services) asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
			jobID, _ := asynq.GetTaskID(ctx)
			attemptNumber, _ := asynq.GetRetryCount(ctx)
			ctx = aicall.WithJob(ctx, jobID, attemptNumber)
			if services.DataReady != nil {
				if err := services.DataReady(ctx); err != nil {
					return err
				}
			}
			if services.Jobs == nil {
				return next.ProcessTask(ctx, task)
			}
			var scope taskScope
			if err := json.Unmarshal(task.Payload(), &scope); err != nil {
				return err
			}
			bound, err := bindTaskScope(ctx, task, services)
			obsolete := errors.Is(err, domain.ErrMemoryTaskObsolete)
			if err != nil && !obsolete {
				return err
			}
			id, ok := asynq.GetTaskID(ctx)
			if !ok {
				id = domain.AccessID("job", task.Type()+string(task.Payload()))
			}
			attempt, _ := asynq.GetRetryCount(ctx)
			maxRetry, _ := asynq.GetMaxRetry(ctx)
			queue, _ := asynq.GetQueueName(ctx)
			job := domain.MemoryJob{GenerationID: scope.GenerationID, Queue: queue, OperationID: scope.OperationID, ID: id, UserID: scope.UserID, WorkspaceID: scope.WorkspaceID, PartitionID: security.WritePartition(bound), ConversationID: scope.ConversationID, TaskType: task.Type(), Status: "processing", Attempt: attempt}
			if obsolete {
				job.Status = "skipped"
				job.FailureCode = "source_obsolete"
				return services.Jobs.RecordJob(bound, job)
			}
			if err = services.Jobs.RecordJob(bound, job); err != nil {
				return err
			}
			err = next.ProcessTask(bound, task)
			job.Status = "complete"
			if errors.Is(err, domain.ErrMemoryTaskObsolete) {
				job.Status = "skipped"
				job.FailureCode = "source_obsolete"
				err = nil
			} else if err != nil {
				job.Status = "failed"
				job.FailureCode = "processing_failed"
				if attempt >= maxRetry || errors.Is(err, asynq.SkipRetry) {
					job.Status = "dead_letter"
				} else {
					next := time.Now().UTC().Add(MemoryRetryDelay(attempt, err, task))
					job.NextRetryAt = &next
				}
			}
			return errors.Join(err, services.Jobs.RecordJob(bound, job))
		})
	}
}
