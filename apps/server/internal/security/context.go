package security

import (
	"context"

	"memstation/apps/server/internal/domain"
)

type contextKey string

const (
	userIDContextKey contextKey = "memstation.user_id"
	adminContextKey  contextKey = "memstation.admin"
)

func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

func UserIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(userIDContextKey).(string)
	return value
}

func WithAdmin(ctx context.Context, isAdmin bool) context.Context {
	return context.WithValue(ctx, adminContextKey, isAdmin)
}

func AdminFromContext(ctx context.Context) bool {
	value, _ := ctx.Value(adminContextKey).(bool)
	return value
}

const (
	accessContextKey contextKey = "memstation.access"
	accessRefreshKey contextKey = "memstation.access_refresh"
)

type accessRefresh func(context.Context) (domain.AccessContext, error)

func WithAccess(ctx context.Context, access domain.AccessContext) context.Context {
	access.Capabilities = append([]string{}, access.Capabilities...)
	return context.WithValue(WithUserID(ctx, access.UserID), accessContextKey, access)
}

func AccessFromContext(ctx context.Context) (domain.AccessContext, bool) {
	value, ok := ctx.Value(accessContextKey).(domain.AccessContext)
	value.Capabilities = append([]string{}, value.Capabilities...)
	return value, ok
}

func RequireCapability(ctx context.Context, capability string) error {
	access, ok := AccessFromContext(ctx)
	// Internal worker and admin services have their own entry-point authorization.
	if !ok {
		return nil
	}
	if refresh, ok := ctx.Value(accessRefreshKey).(accessRefresh); ok {
		var err error
		access, err = refresh(ctx)
		if err != nil {
			return domain.ErrAccessDenied
		}
	}
	if !access.Allows(capability) {
		return domain.ErrAccessDenied
	}
	return nil
}
