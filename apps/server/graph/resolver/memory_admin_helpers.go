package resolver

import (
	"context"
	"errors"

	"memstation/apps/server/graph/generated"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/identity"
	"memstation/apps/server/internal/service/ingestion"
	"memstation/apps/server/internal/service/lifecycle"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

func (r *Resolver) memoryAdminReady(ctx context.Context) error {
	if !security.AdminFromContext(ctx) {
		return memoryGraphError(domain.ErrAccessDenied)
	}
	if r.Identity == nil || r.Lifecycle == nil || r.Ingestion == nil || r.MemoryControlService == nil || r.AIUsage == nil {
		return memoryGraphError(errors.New("unavailable"))
	}
	return nil
}

func (r *Resolver) memoryAdminScope(ctx context.Context, in generated.WorkspaceScope) (context.Context, error) {
	if err := r.memoryAdminReady(ctx); err != nil {
		return ctx, err
	}
	bound, err := r.Identity.AdministrativeDataContext(ctx, in.UserID, in.WorkspaceID, derefString(in.PartitionID))
	if err != nil {
		return ctx, memoryGraphError(err)
	}
	if derefString(in.PartitionID) != "" && r.AuditService != nil {
		r.AuditService.Record(bound, domain.AuditEvent{ActorType: "admin", ActorID: "admin", Action: "inspect_private_workspace", TargetType: "workspace", TargetID: in.WorkspaceID, Payload: map[string]any{"partition_id": derefString(in.PartitionID)}})
	}
	return bound, nil
}

func memoryGraphValue[T any](value T, err error) (*T, error) {
	if err != nil {
		return nil, memoryGraphError(err)
	}
	return &value, nil
}

func memoryGraphList[T any](items []T, err error) ([]*T, error) {
	if err != nil {
		return nil, memoryGraphError(err)
	}
	out := make([]*T, 0, len(items))
	for _, v := range items {
		out = append(out, &v)
	}
	return out, nil
}

func memoryGraphError(err error) error {
	if err == nil {
		return nil
	}
	code := "memory_unavailable"
	switch {
	case errors.Is(err, domain.ErrAccessDenied):
		code = "access_denied"
	case errors.Is(err, domain.ErrAccessNotFound):
		code = "not_found"
	case errors.Is(err, domain.ErrAccessConflict):
		code = "revision_conflict"
	case errors.Is(err, ingestion.ErrIdempotencyConflict):
		code = "idempotency_conflict"
	case errors.Is(err, lifecycle.ErrInvalidInput), errors.Is(err, identity.ErrInvalidInput), errors.Is(err, ingestion.ErrInvalidEvent):
		code = "invalid_request"
	}
	return &gqlerror.Error{Message: code, Extensions: map[string]any{"code": code}}
}
