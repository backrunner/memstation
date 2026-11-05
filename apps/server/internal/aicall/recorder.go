package aicall

import (
	"context"
	"log/slog"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

type Recorder struct {
	Ledger  ports.AICallRepository
	Call    domain.AICall
	started time.Time
}

func Start(ctx context.Context, ledger ports.AICallRepository, user, role, model, authority string) (*Recorder, error) {
	if ledger == nil {
		return &Recorder{}, nil
	}
	workspace, err := security.DataScope(ctx, user, "")
	if err != nil {
		return nil, err
	}
	meta, job := MetadataFromContext(ctx), JobFromContext(ctx)
	if meta.Stage == "" {
		meta.Stage = role
	}
	r := &Recorder{Ledger: ledger, started: time.Now(), Call: domain.AICall{ID: uuid.NewString(), UserID: user, WorkspaceID: workspace, PartitionID: security.WritePartition(ctx), PartitionIDs: security.Partitions(ctx), Role: role, Stage: meta.Stage, Strategy: meta.Strategy, ModelAlias: model, UsageAuthority: authority, Status: "unknown", JobID: job.ID, Attempt: job.Attempt, TraceID: trace.SpanContextFromContext(ctx).TraceID().String()}}
	// Usage accounting must not take down the call it records.
	if err = ledger.StartAICall(ctx, r.Call); err != nil {
		slog.WarnContext(ctx, "ai call ledger start failed", "role", role, "error", err)
		return &Recorder{}, nil
	}
	return r, nil
}

func (r *Recorder) Finish(ctx context.Context, callErr error) error {
	if r == nil || r.Ledger == nil {
		return nil
	}
	r.Call.DurationMS = time.Since(r.started).Milliseconds()
	if callErr == nil {
		r.Call.Status = "complete"
	} else {
		r.Call.FailureCode = "provider_result_unknown"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.Ledger.FinishAICall(ctx, r.Call); err != nil {
		slog.WarnContext(ctx, "ai call ledger finish failed", "role", r.Call.Role, "error", err)
	}
	Record(ctx, r.Call)
	return nil
}
