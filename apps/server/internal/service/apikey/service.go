package apikey

import (
	"context"
	"fmt"
	"strings"
	"time"

	identitystore "memstation/apps/server/internal/store/sql/identity"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/identity"

	"github.com/google/uuid"
)

type Record = domain.Credential

type CreateOptions struct {
	WorkspaceID  string     `json:"workspace_id"`
	Name         string     `json:"name"`
	AgentID      string     `json:"agent_id"`
	Capabilities []string   `json:"capabilities,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type Service struct {
	repo       ports.IdentityRepository
	identities *identity.Service
}

func NewService(db runtimeconfig.DBHandle) *Service {
	repo := identitystore.NewIdentityRepository(db)
	return &Service{repo: repo, identities: identity.NewService(repo)}
}
func (s *Service) Identities() *identity.Service { return s.identities }

func (s *Service) CreateWithOptions(ctx context.Context, user string, input CreateOptions) (Record, string, error) {
	input.Name = strings.TrimSpace(input.Name)
	if user == "" || strings.TrimSpace(input.AgentID) == "" || strings.TrimSpace(input.WorkspaceID) == "" || !identity.ValidateName(input.Name) || input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		return Record{}, "", identity.ErrInvalidInput
	}
	if err := identity.ValidateCapabilities(input.Capabilities); err != nil {
		return Record{}, "", err
	}
	w, err := s.identities.Workspace(ctx, user, input.WorkspaceID)
	if err != nil {
		return Record{}, "", err
	}
	if w.Status != "active" {
		return Record{}, "", domain.ErrAccessDenied
	}
	grant, err := s.identities.WorkspaceGrant(ctx, user, w.ID, input.AgentID)
	if err != nil {
		return Record{}, "", err
	}
	if input.Capabilities == nil {
		input.Capabilities = append([]string{}, grant.Capabilities...)
	}
	// The immutable ceiling may only be issued within today's grant and policy.
	effective := domain.EffectiveCapabilities(input.Capabilities, grant.Capabilities, w.Capabilities)
	if len(effective) != len(input.Capabilities) {
		return Record{}, "", domain.ErrAccessDenied
	}
	raw := "ms-" + uuid.NewString()
	prefix, hash, err := PrefixAndHash(raw)
	if err != nil {
		return Record{}, "", err
	}
	record := Record{ID: uuid.NewString(), UserID: user, AgentID: input.AgentID, DefaultWorkspaceID: w.ID, Name: input.Name, Prefix: prefix, Hash: hash, Capabilities: effective, ExpiresAt: input.ExpiresAt, CreatedAt: time.Now().UTC()}
	if err = s.repo.CreateCredential(ctx, record); err != nil {
		return Record{}, "", err
	}
	record.Hash = ""
	return record, raw, nil
}

// Revoke is reserved for admin transports that already operate by global ID.
func (s *Service) Revoke(ctx context.Context, id string) error {
	return s.repo.RevokeCredential(ctx, "", id)
}

func (s *Service) RevokeForUser(ctx context.Context, user, id string) error {
	if user == "" {
		return domain.ErrAccessDenied
	}
	return s.repo.RevokeCredential(ctx, user, id)
}

func (s *Service) LookupPrincipalByHash(ctx context.Context, prefix, hash string) (domain.AccessContext, error) {
	return s.repo.Authenticate(ctx, prefix, hash)
}

func (s *Service) List(ctx context.Context) ([]Record, error) { return s.repo.ListCredentials(ctx) }
func PrefixAndHash(raw string) (string, string, error) {
	prefix, hash, err := (security.APIKeyHasher{}).Hash(raw)
	if err != nil {
		return "", "", err
	}
	if prefix == "" || hash == "" {
		return "", "", fmt.Errorf("invalid api key")
	}
	return prefix, hash, nil
}

func (s *Service) SetFallbackAudit(record func(context.Context, domain.AuditEvent)) {
	if repo, ok := s.repo.(interface {
		SetFallbackAudit(func(context.Context, domain.AuditEvent))
	}); ok {
		repo.SetFallbackAudit(record)
	}
}

// ListForAgent returns bounded, owner-scoped metadata. Effective capabilities are
// a display snapshot; request authentication always checks the current authority.
func (s *Service) ListForAgent(ctx context.Context, user, agent, after string, limit int) ([]domain.CredentialAccess, error) {
	d, err := s.identities.Detail(ctx, user, agent)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	records, err := s.repo.ListAgentCredentials(ctx, user, agent, after, limit)
	if err != nil {
		return nil, err
	}
	items := make([]domain.CredentialAccess, 0, len(records))
	now := time.Now()
	for _, k := range records {
		caps := []string{}
		if k.RevokedAt == nil && (k.ExpiresAt == nil || k.ExpiresAt.After(now)) {
			grant, grantErr := s.identities.WorkspaceGrant(ctx, user, k.DefaultWorkspaceID, agent)
			workspace, workspaceErr := s.identities.Workspace(ctx, user, k.DefaultWorkspaceID)
			if grantErr == nil && workspaceErr == nil && d.Agent.Status == "active" && workspace.Status == "active" {
				caps = domain.EffectiveCapabilities(k.Capabilities, grant.Capabilities, workspace.Capabilities)
			}
		}
		k.Hash = ""
		items = append(items, domain.CredentialAccess{Credential: k, EffectiveCapabilities: caps})
	}
	return items, nil
}

func (s *Service) ResolveAccess(ctx context.Context, p domain.AccessContext, workspace string, audience domain.Audience) (domain.AccessContext, error) {
	repo, ok := s.repo.(ports.WorkspaceAdministration)
	if !ok {
		return domain.AccessContext{}, domain.ErrAccessDenied
	}
	return repo.ResolveAccess(ctx, p, workspace, audience)
}
