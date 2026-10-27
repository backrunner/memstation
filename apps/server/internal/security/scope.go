package security

import (
	"context"
	"slices"

	"memstation/apps/server/internal/domain"
)

type dataScope struct {
	UserID, WorkspaceID string
	PartitionIDs        []string
}

const dataScopeKey contextKey = "memstation.data_scope"

// PersonalScope restricts an explicitly Personal operation to its owner.
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
	return context.WithValue(ctx, dataScopeKey, dataScope{UserID: user, WorkspaceID: resolved, PartitionIDs: []string{""}}), nil
}

// DataScope resolves only server-bound authority. An explicit request parameter
// is a consistency assertion, never permission to select another workspace.
func DataScope(ctx context.Context, user, explicit string) (string, error) {
	if user == "" {
		return "", domain.ErrAccessDenied
	}
	workspace := domain.PersonalWorkspaceID(user)
	if scope, ok := ctx.Value(dataScopeKey).(dataScope); ok {
		if scope.UserID != user || scope.WorkspaceID == "" {
			return "", domain.ErrAccessDenied
		}
		workspace = scope.WorkspaceID
	}
	if access, ok := AccessFromContext(ctx); ok {
		if access.UserID != user || access.WorkspaceID == "" || access.ScopeVersion != 2 {
			return "", domain.ErrAccessDenied
		}
		if scope, bound := ctx.Value(dataScopeKey).(dataScope); bound && scope.WorkspaceID != access.WorkspaceID {
			return "", domain.ErrAccessDenied
		}
		workspace = access.WorkspaceID
	}
	if explicit != "" && explicit != workspace {
		return "", domain.ErrAccessDenied
	}
	return workspace, nil
}

// WithDataScope is for an already verified administrator or durable worker job.
// Transports must authenticate and resolve the workspace before calling it.
func WithDataScope(ctx context.Context, user, workspace string, partitions ...string) (context.Context, error) {
	if user == "" || workspace == "" {
		return ctx, domain.ErrAccessDenied
	}
	if len(partitions) == 0 {
		partitions = []string{""}
	}
	if access, ok := AccessFromContext(ctx); ok {
		if access.UserID != user || access.WorkspaceID != workspace || access.ScopeVersion != 2 {
			return ctx, domain.ErrAccessDenied
		}
		for _, partition := range partitions {
			if !slices.Contains(Partitions(ctx), partition) {
				return ctx, domain.ErrAccessDenied
			}
		}
	}
	if prior, ok := ctx.Value(dataScopeKey).(dataScope); ok {
		if prior.UserID != user || prior.WorkspaceID != workspace {
			return ctx, domain.ErrAccessDenied
		}
		for _, partition := range partitions {
			if !slices.Contains(prior.PartitionIDs, partition) {
				return ctx, domain.ErrAccessDenied
			}
		}
	}
	return context.WithValue(ctx, dataScopeKey, dataScope{UserID: user, WorkspaceID: workspace, PartitionIDs: slices.Clone(partitions)}), nil
}

// WorkspaceID is for keys/SQL after DataScope has validated the entry point.
// Invalid contexts resolve to an empty value and can never widen a query.
func WorkspaceID(ctx context.Context, user string) string {
	workspace, _ := DataScope(ctx, user, "")
	return workspace
}

func Partitions(ctx context.Context) []string {
	if scope, ok := ctx.Value(dataScopeKey).(dataScope); ok {
		return slices.Clone(scope.PartitionIDs)
	}
	if access, ok := AccessFromContext(ctx); ok && access.ScopeVersion == 2 {
		return slices.Clone(access.PartitionIDs)
	}
	return []string{""}
}

func CanReadPartition(ctx context.Context, partition string) bool {
	return slices.Contains(Partitions(ctx), partition)
}

func BoundWorkspace(ctx context.Context) string {
	if scope, ok := ctx.Value(dataScopeKey).(dataScope); ok {
		return scope.WorkspaceID
	}
	if access, ok := AccessFromContext(ctx); ok {
		workspace, _ := DataScope(ctx, access.UserID, "")
		return workspace
	}
	return ""
}

func WritePartition(ctx context.Context) string {
	parts := Partitions(ctx)
	if len(parts) == 1 {
		return parts[0]
	}
	return ""
}

// ScopedIdentity includes the workspace and partition for every record.
func ScopedIdentity(ctx context.Context, user string) string {
	workspace := WorkspaceID(ctx, user)
	partition := WritePartition(ctx)
	return domain.AccessID("scope", user+"\x00"+workspace+"\x00"+partition)
}

func WorkspaceIdentity(ctx context.Context, user string) string {
	workspace := WorkspaceID(ctx, user)
	return domain.AccessID("workspace-scope", user+"\x00"+workspace)
}

// BindAdministrativeWorkspace is reserved for explicit, audited admin paths.
// The caller must resolve the workspace and partitions before binding them.
func BindAdministrativeWorkspace(ctx context.Context, user, workspace string, partitions ...string) (context.Context, error) {
	if !AdminFromContext(ctx) || user == "" || workspace == "" {
		return ctx, domain.ErrAccessDenied
	}
	if len(partitions) == 0 {
		partitions = []string{""}
	}
	ctx = context.WithValue(ctx, dataScopeKey, nil)
	ctx = context.WithValue(ctx, accessRefreshKey, nil)
	ctx = WithAccess(ctx, domain.AccessContext{UserID: user, WorkspaceID: workspace, AgentID: "admin", CredentialID: "admin-session", ScopeVersion: 2, PartitionIDs: partitions, Capabilities: domain.AllCapabilities(), Audience: domain.Audience{Kind: "admin_inspection"}})
	return WithDataScope(ctx, user, workspace, partitions...)
}

// MaintenanceContext is reserved for deployment recovery from the authenticated
// deletion journal. It removes a triggering request's principal; each operation
// then binds its scope from the persisted authority, never from request fields.
func MaintenanceContext(ctx context.Context) context.Context {
	for _, key := range []contextKey{dataScopeKey, accessContextKey, accessRefreshKey, userIDContextKey, adminContextKey} {
		ctx = context.WithValue(ctx, key, nil)
	}
	return ctx
}
