package aicall

import (
	"context"
	"errors"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

// Adapter records one provider-adapter invocation, including truncation retries.
// It never estimates tokens or costs from request size or configured budgets.
type Adapter struct {
	Base   ports.ProviderAdapter
	Ledger ports.AICallRepository
}

func (a Adapter) Name() domain.Provider { return a.Base.Name() }
func (a Adapter) Stream(context.Context, domain.UnifiedRequest, func(domain.StreamEvent) error) (*domain.UnifiedResponse, error) {
	return nil, domain.ErrAccessDenied
}

func (a Adapter) Complete(ctx context.Context, request domain.UnifiedRequest) (out *domain.UnifiedResponse, err error) {
	workspace, err := security.DataScope(ctx, request.UserID, "")
	if err != nil {
		return nil, err
	}
	job, meta := JobFromContext(ctx), MetadataFromContext(ctx)
	call := domain.AICall{ID: uuid.NewString(), UserID: request.UserID, WorkspaceID: workspace, PartitionID: security.WritePartition(ctx), PartitionIDs: security.Partitions(ctx), Role: meta.Stage, Stage: meta.Stage, Strategy: meta.Strategy, ModelAlias: request.Model, JobID: job.ID, Attempt: job.Attempt, UsageAuthority: "memstation", Status: "unknown", TraceID: trace.SpanContextFromContext(ctx).TraceID().String()}
	if err = a.Ledger.StartAICall(ctx, call); err != nil {
		return nil, err
	}
	start := time.Now()
	defer func() {
		call.DurationMS = time.Since(start).Milliseconds()
		if out != nil {
			call.ProviderRequestID = out.ResponseID
			call.ModelVersion = out.Model
			call.InputTokens = out.Usage.InputTokens
			call.OutputTokens = out.Usage.OutputTokens
			// A positive provider usage record is known; absent/zero usage stays unknown.
			call.UsageKnown = out.Usage.TotalTokens > 0 || out.Usage.InputTokens > 0 || out.Usage.OutputTokens > 0
		}
		if err == nil {
			call.Status = "complete"
		} else {
			call.FailureCode = "provider_result_unknown"
		}
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, a.Ledger.FinishAICall(recordCtx, call))
		Record(ctx, call)
	}()
	return a.Base.Complete(ctx, request)
}
