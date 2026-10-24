package security

import (
	"context"

	"memstation/apps/server/internal/domain"
)

type dataScope struct{ UserID, WorkspaceID string }

const dataScopeKey contextKey = "memstation.data_scope"

// PersonalScope is the compatibility boundary while the remaining memory data
// plane is migrated. Explicit unsupported scopes fail closed, never widen to a
// user-wide query. Missing scope is supported only for trusted legacy callers.
func PersonalScope(ctx context.Context, user, explicit string) (string, error) {
	if user == "" {
		return "", domain.ErrAccessDenied
	}
	workspace := domain.PersonalWorkspaceID(user)
	if explicit != "" && explicit != workspace {
		return "", domain.ErrAccessDenied
	}
	if access, ok := AccessFromContext(ctx); ok && (access.UserID != user || access.WorkspaceID != workspace) {
		return "", domain.ErrAccessDenied
	}
	if scope, ok := ctx.Value(dataScopeKey).(dataScope); ok && (scope.UserID != user || scope.WorkspaceID != workspace) {
		return "", domain.ErrAccessDenied
	}
	return workspace, nil
}

// WithPersonalDataScope binds a verified worker scope, not an API capability.
func WithPersonalDataScope(ctx context.Context, user, workspace string) (context.Context, error) {
	resolved, err := PersonalScope(ctx, user, workspace)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, dataScopeKey, dataScope{UserID: user, WorkspaceID: resolved}), nil
}
