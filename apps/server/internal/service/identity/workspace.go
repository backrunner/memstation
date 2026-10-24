package identity

import (
	"context"
	"strings"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

// Workspaces is an explicit administrator query. It never provisions an owner
// or widens a proxy caller's scope. Only the supported Personal data plane is listed.
func (s *Service) Workspaces(ctx context.Context, q domain.WorkspaceQuery) (domain.WorkspacePage, error) {
	page := domain.WorkspacePage{Items: []domain.Workspace{}, WorkspaceMode: "personal_only"}
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
	ctx, err = security.WithPersonalDataScope(ctx, w.UserID, w.ID)
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
	if w.Kind != "personal" || w.ID != domain.PersonalWorkspaceID(w.UserID) {
		return domain.Workspace{}, domain.ErrAccessNotFound
	}
	return w, nil
}

func (s *Service) UpdateWorkspace(ctx context.Context, w domain.Workspace) (domain.Workspace, error) {
	w.Name = strings.TrimSpace(w.Name)
	if w.UserID == "" || w.ID != domain.PersonalWorkspaceID(w.UserID) || w.PolicyVersion < 1 || !ValidateName(w.Name) || w.Capabilities == nil {
		return domain.Workspace{}, ErrInvalidInput
	}
	if err := ValidateCapabilities(w.Capabilities); err != nil {
		return domain.Workspace{}, err
	}
	return s.repo.UpdateWorkspace(ctx, w)
}
