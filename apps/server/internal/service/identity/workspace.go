package identity

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
)

// Workspaces is an explicit administrator query. It never provisions an owner
// or widens a proxy caller's scope. Only the supported Personal data plane is listed.
func (s *Service) Workspaces(ctx context.Context, q domain.WorkspaceQuery) (domain.WorkspacePage, error) {
	page := domain.WorkspacePage{Items: []domain.Workspace{}, WorkspaceMode: "workspace"}
	q.Query = strings.TrimSpace(q.Query)
	if utf8.RuneCountInString(q.Query) > 120 || len(q.UserID) > 256 || len(q.After) > 256 || q.Limit < 0 || q.Limit > 100 {
		return page, ErrInvalidInput
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	limit := q.Limit
	q.Limit++
	items, err := s.repo.SearchWorkspaces(ctx, q)
	if err != nil {
		return page, err
	}
	if len(items) > limit {
		items = items[:limit]
		page.NextCursor = items[len(items)-1].ID
	}
	page.Items = items
	return page, nil
}

// WorkspaceDataContext resolves an existing administrator-selected workspace.
// It binds the data plane without granting an Agent capability or provisioning
// a missing owner. Unsupported workspace kinds remain closed during migration.
func (s *Service) WorkspaceDataContext(ctx context.Context, user, id string) (context.Context, domain.Workspace, error) {
	w, err := s.Workspace(ctx, user, id)
	if err != nil {
		return ctx, w, err
	}
	ctx, err = security.WithDataScope(ctx, w.UserID, w.ID)
	return ctx, w, err
}

// An empty owner is permitted only for explicit administrator discovery.
func (s *Service) Workspace(ctx context.Context, user, id string) (domain.Workspace, error) {
	if id == "" || len(id) > 256 || len(user) > 256 {
		return domain.Workspace{}, ErrInvalidInput
	}
	w, err := s.repo.GetWorkspace(ctx, user, id)
	if err != nil {
		return w, err
	}
	if w.Kind != "personal" && w.Kind != "group" && w.Kind != "project" {
		return domain.Workspace{}, domain.ErrAccessNotFound
	}
	return w, nil
}

func (s *Service) UpdateWorkspace(ctx context.Context, w domain.Workspace) (domain.Workspace, error) {
	w.Name = strings.TrimSpace(w.Name)
	if w.UserID == "" || w.ID == "" || w.PolicyVersion < 1 || !ValidateName(w.Name) || w.Capabilities == nil {
		return domain.Workspace{}, ErrInvalidInput
	}
	if err := ValidateCapabilities(w.Capabilities); err != nil {
		return domain.Workspace{}, err
	}
	return s.repo.UpdateWorkspace(ctx, w)
}

func (s *Service) administrator() (ports.WorkspaceAdministration, error) {
	r, ok := s.repo.(ports.WorkspaceAdministration)
	if !ok {
		return nil, domain.ErrAccessDenied
	}
	return r, nil
}

func (s *Service) CreateWorkspace(ctx context.Context, w domain.Workspace) (domain.Workspace, error) {
	w.Name = strings.TrimSpace(w.Name)
	if w.UserID == "" || !ValidateName(w.Name) || (w.Kind != "group" && w.Kind != "project") {
		return w, ErrInvalidInput
	}
	if w.Capabilities == nil {
		w.Capabilities = []string{domain.CapabilityMemoryRead, domain.CapabilityEventsWrite, domain.CapabilitySourcesRead, domain.CapabilitySourcesRetract, domain.CapabilityCandidatesWrite, domain.CapabilityMemoryForget, domain.CapabilityMemoryExport, domain.CapabilityUsageRead}
	}
	if err := ValidateCapabilities(w.Capabilities); err != nil {
		return w, err
	}
	repo, err := s.administrator()
	if err != nil {
		return w, err
	}
	return repo.CreateWorkspace(ctx, w)
}

func (s *Service) PutIntegration(ctx context.Context, input domain.IntegrationConfig) (domain.IntegrationConfig, error) {
	if input.AllowedEventTypes == nil {
		input.AllowedEventTypes = []string{}
	}
	if !ValidateName(input.Name) || input.Kind == "" || len(input.Kind) > 64 || input.AgentID == "" || input.ExternalSpaceID == "" || len(input.ExternalSpaceID) > 256 || input.Revision < 0 {
		return input, ErrInvalidInput
	}
	if _, err := s.Workspace(ctx, input.UserID, input.WorkspaceID); err != nil {
		return input, err
	}
	if _, err := s.repo.GetGrant(ctx, input.UserID, input.WorkspaceID, input.AgentID); err != nil {
		return input, err
	}
	for _, kind := range input.AllowedEventTypes {
		if !slices.Contains([]string{"message.created", "message.edited", "message.deleted"}, kind) {
			return input, ErrInvalidInput
		}
	}
	repo, err := s.administrator()
	if err != nil {
		return input, err
	}
	return repo.PutIntegration(ctx, input)
}

func (s *Service) Integrations(ctx context.Context, user, workspace string) ([]domain.IntegrationConfig, error) {
	if _, err := s.Workspace(ctx, user, workspace); err != nil {
		return nil, err
	}
	repo, err := s.administrator()
	if err != nil {
		return nil, err
	}
	return repo.ListIntegrations(ctx, user, workspace)
}

func (s *Service) PutPartition(ctx context.Context, p domain.MemoryPartition) (domain.MemoryPartition, error) {
	if p.UserID == "" || p.WorkspaceID == "" || p.AgentID == "" || p.Revision < 0 || (p.Revision == 0 && len(p.AudienceActorIDs) == 0) || len(p.AudienceActorIDs) > 64 {
		return p, ErrInvalidInput
	}
	if _, err := s.repo.GetGrant(ctx, p.UserID, p.WorkspaceID, p.AgentID); err != nil {
		return p, err
	}
	repo, err := s.administrator()
	if err != nil {
		return p, err
	}
	return repo.PutPartition(ctx, p)
}

func (s *Service) Partitions(ctx context.Context, user, workspace string) ([]domain.MemoryPartition, error) {
	if _, err := s.Workspace(ctx, user, workspace); err != nil {
		return nil, err
	}
	repo, err := s.administrator()
	if err != nil {
		return nil, err
	}
	return repo.ListPartitions(ctx, user, workspace)
}

func (s *Service) WorkspaceGrant(ctx context.Context, user, workspace, agent string) (domain.WorkspaceGrant, error) {
	return s.repo.GetGrant(ctx, user, workspace, agent)
}

// AdministrativeDataContext requires an explicit private partition selection.
// The returned scope is suitable for all transports; it grants no Agent credentials.
func (s *Service) AdministrativeDataContext(ctx context.Context, user, workspace, partition string) (context.Context, error) {
	if !security.AdminFromContext(ctx) {
		return ctx, domain.ErrAccessDenied
	}
	w, err := s.Workspace(ctx, user, workspace)
	if err != nil {
		return ctx, err
	}
	if partition != "" {
		partitions, err := s.Partitions(ctx, w.UserID, w.ID)
		if err != nil {
			return ctx, err
		}
		found := false
		for _, p := range partitions {
			if p.ID == partition {
				found = true
			}
		}
		if !found {
			return ctx, domain.ErrAccessNotFound
		}
	}
	return security.BindAdministrativeWorkspace(ctx, w.UserID, w.ID, partition)
}

func (s *Service) WorkspaceAgentDetail(ctx context.Context, user, workspace, agent string) (domain.AgentDetail, error) {
	if !security.AdminFromContext(ctx) {
		return domain.AgentDetail{}, domain.ErrAccessDenied
	}
	a, err := s.repo.GetAgent(ctx, user, agent)
	if err != nil {
		return domain.AgentDetail{}, err
	}
	w, err := s.Workspace(ctx, user, workspace)
	if err != nil {
		return domain.AgentDetail{}, err
	}
	g, err := s.repo.GetGrant(ctx, user, workspace, agent)
	if errors.Is(err, domain.ErrAccessNotFound) {
		g = domain.WorkspaceGrant{UserID: user, WorkspaceID: workspace, AgentID: agent, Capabilities: []string{}}
	} else if err != nil {
		return domain.AgentDetail{}, err
	}
	caps := domain.EffectiveCapabilities(domain.AllCapabilities(), g.Capabilities, w.Capabilities)
	if a.Status != "active" || w.Status != "active" {
		caps = []string{}
	}
	return domain.AgentDetail{Agent: a, Workspace: w, Grant: g, EffectiveCapabilities: caps}, nil
}

func (s *Service) CreateWorkspaceAgent(ctx context.Context, user, workspace, name string, caps []string) (domain.Agent, error) {
	if !security.AdminFromContext(ctx) {
		return domain.Agent{}, domain.ErrAccessDenied
	}
	w, err := s.Workspace(ctx, user, workspace)
	if err != nil {
		return domain.Agent{}, err
	}
	if w.Status != "active" {
		return domain.Agent{}, domain.ErrAccessDenied
	}
	if !ValidateName(name) || ValidateCapabilities(caps) != nil {
		return domain.Agent{}, ErrInvalidInput
	}
	if caps == nil {
		caps = []string{}
	}
	now := time.Now().UTC()
	a := domain.Agent{ID: uuid.NewString(), UserID: user, ActorID: uuid.NewString(), Name: strings.TrimSpace(name), Status: "active", DefaultWorkspaceID: w.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
	err = s.repo.CreateAgent(ctx, a, domain.WorkspaceGrant{UserID: user, WorkspaceID: w.ID, AgentID: a.ID, Capabilities: caps, Revision: 1})
	return a, err
}
