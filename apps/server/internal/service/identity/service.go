package identity

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"

	"github.com/google/uuid"
)

var ErrInvalidInput = errors.New("invalid identity input")

type Service struct{ repo ports.IdentityRepository }

func NewService(repo ports.IdentityRepository) *Service { return &Service{repo: repo} }
func ValidateName(name string) bool {
	return strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= 120
}

func ValidateCapabilities(caps []string) error {
	if len(caps) > len(domain.PersonalCapabilities()) {
		return ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, capability := range caps {
		if !slices.Contains(domain.PersonalCapabilities(), capability) || seen[capability] {
			return ErrInvalidInput
		}
		seen[capability] = true
	}
	return nil
}

func (s *Service) Personal(ctx context.Context, user string) (domain.Workspace, error) {
	return s.repo.EnsurePersonal(ctx, user)
}

func (s *Service) Create(ctx context.Context, user, name string, caps []string) (domain.Agent, error) {
	name = strings.TrimSpace(name)
	if user == "" || !ValidateName(name) {
		return domain.Agent{}, ErrInvalidInput
	}
	if caps == nil {
		caps = []string{domain.CapabilityGateway, domain.CapabilityMemoryRead, domain.CapabilityFilesRead}
	}
	if err := ValidateCapabilities(caps); err != nil {
		return domain.Agent{}, err
	}
	w, err := s.repo.EnsurePersonal(ctx, user)
	if err != nil {
		return domain.Agent{}, err
	}
	if w.Status != "active" {
		return domain.Agent{}, domain.ErrAccessDenied
	}
	now := time.Now().UTC()
	a := domain.Agent{ID: uuid.NewString(), UserID: user, ActorID: uuid.NewString(), Name: name, Status: "active", DefaultWorkspaceID: w.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
	err = s.repo.CreateAgent(ctx, a, domain.WorkspaceGrant{UserID: user, WorkspaceID: w.ID, AgentID: a.ID, Capabilities: caps, Revision: 1})
	return a, err
}

func (s *Service) List(ctx context.Context, user, after string, limit int) ([]domain.Agent, error) {
	if user == "" {
		return nil, ErrInvalidInput
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if _, err := s.repo.EnsurePersonal(ctx, user); err != nil {
		return nil, err
	}
	return s.repo.ListAgents(ctx, user, after, limit)
}

func (s *Service) Update(ctx context.Context, a domain.Agent) (domain.Agent, error) {
	a.Name = strings.TrimSpace(a.Name)
	if a.UserID == "" || a.ID == "" || a.Revision < 1 || !ValidateName(a.Name) || (a.Status != "active" && a.Status != "disabled") {
		return domain.Agent{}, ErrInvalidInput
	}
	return s.repo.UpdateAgent(ctx, a)
}

func (s *Service) Grant(ctx context.Context, user, agent string) (domain.WorkspaceGrant, error) {
	return s.repo.GetGrant(ctx, user, domain.PersonalWorkspaceID(user), agent)
}

func (s *Service) SetGrant(ctx context.Context, g domain.WorkspaceGrant) (domain.WorkspaceGrant, error) {
	if g.UserID == "" || g.AgentID == "" || g.Revision < 1 || g.WorkspaceID != domain.PersonalWorkspaceID(g.UserID) {
		return g, ErrInvalidInput
	}
	if err := ValidateCapabilities(g.Capabilities); err != nil {
		return g, err
	}
	if g.Capabilities == nil {
		g.Capabilities = []string{}
	}
	return s.repo.PutGrant(ctx, g)
}

func (s *Service) Search(ctx context.Context, q domain.AgentQuery) ([]domain.Agent, error) {
	q.Query = strings.TrimSpace(q.Query)
	if q.UserID == "" || utf8.RuneCountInString(q.Query) > 120 || (q.Status != "" && q.Status != "active" && q.Status != "disabled") {
		return nil, ErrInvalidInput
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	if _, err := s.repo.EnsurePersonal(ctx, q.UserID); err != nil {
		return nil, err
	}
	return s.repo.SearchAgents(ctx, q)
}

func (s *Service) Detail(ctx context.Context, user, id string) (domain.AgentDetail, error) {
	var detail domain.AgentDetail
	a, err := s.repo.GetAgent(ctx, user, id)
	if err != nil {
		return detail, err
	}
	g, err := s.Grant(ctx, user, id)
	if err != nil {
		return detail, err
	}
	w, err := s.Personal(ctx, user)
	if err != nil {
		return detail, err
	}
	caps := []string{}
	if a.Status == "active" && w.Status == "active" {
		caps = domain.EffectiveCapabilities(g.Capabilities, g.Capabilities, w.Capabilities)
	}
	return domain.AgentDetail{Agent: a, Grant: g, Workspace: w, EffectiveCapabilities: caps}, nil
}
