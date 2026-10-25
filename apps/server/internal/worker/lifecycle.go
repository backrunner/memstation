package worker

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hibiken/asynq"
)

func handleMemoryLifecycle(s Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		bound, err := bindTaskScope(ctx, t, s)
		if err != nil {
			return err
		}
		var scope taskScope
		if err = json.Unmarshal(t.Payload(), &scope); err != nil {
			return err
		}
		if s.ProcessOperation == nil || scope.OperationID == "" {
			return errors.New("memory lifecycle processor unavailable")
		}
		return s.ProcessOperation(bound, scope.OperationID)
	}
}

func handleIngestionAccepted(s Services) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		bound, err := bindTaskScope(ctx, t, s)
		if err != nil {
			return err
		}
		var scope taskScope
		if err = json.Unmarshal(t.Payload(), &scope); err != nil {
			return err
		}
		if s.AcknowledgeReceipt == nil || scope.ReceiptID == "" {
			return errors.New("ingestion processor unavailable")
		}
		return s.AcknowledgeReceipt(bound, scope.ReceiptID)
	}
}
