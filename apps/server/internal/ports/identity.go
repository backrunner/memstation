package ports

import (
	"context"

	"memstation/apps/server/internal/domain"
)

// IdentityRepository mutations include their audit record in the same transaction.
type IdentityRepository interface {
	EnsurePersonal(context.Context, string) (domain.Workspace, error)
	SearchWorkspaces(context.Context, domain.WorkspaceQuery) ([]domain.Workspace, error)
	GetWorkspace(context.Context, string, string) (domain.Workspace, error)
	UpdateWorkspace(context.Context, domain.Workspace) (domain.Workspace, error)
	CreateAgent(context.Context, domain.Agent, domain.WorkspaceGrant) error
	ListAgents(context.Context, string, string, int) ([]domain.Agent, error)
	SearchAgents(context.Context, domain.AgentQuery) ([]domain.Agent, error)
	GetAgent(context.Context, string, string) (domain.Agent, error)
	ListAgentCredentials(context.Context, string, string, string, int) ([]domain.Credential, error)
	UpdateAgent(context.Context, domain.Agent) (domain.Agent, error)
	GetGrant(context.Context, string, string, string) (domain.WorkspaceGrant, error)
	PutGrant(context.Context, domain.WorkspaceGrant) (domain.WorkspaceGrant, error)
	CreateCredential(context.Context, domain.Credential) error
	RevokeCredential(context.Context, string, string) error
	ListCredentials(context.Context) ([]domain.Credential, error)
	Authenticate(context.Context, string, string) (domain.AccessContext, error)
}

// Administration is separate from runtime authentication; Actors never confer a grant.
type WorkspaceAdministration interface {
	CreateWorkspace(context.Context, domain.Workspace) (domain.Workspace, error)
	PutIntegration(context.Context, domain.IntegrationConfig) (domain.IntegrationConfig, error)
	ListIntegrations(context.Context, string, string) ([]domain.IntegrationConfig, error)
	PutPartition(context.Context, domain.MemoryPartition) (domain.MemoryPartition, error)
	ListPartitions(context.Context, string, string) ([]domain.MemoryPartition, error)
	ResolveAccess(context.Context, domain.AccessContext, string, domain.Audience) (domain.AccessContext, error)
}
