package security

import "context"

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
