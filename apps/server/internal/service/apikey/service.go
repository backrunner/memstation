package apikey

import (
	"context"
	"fmt"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/identity"
	sqlstore "memstation/apps/server/internal/store/sql"

	"github.com/google/uuid"
)

type Record = domain.Credential

type CreateOptions struct {
	Name         string     `json:"name"`
	AgentID      string     `json:"agent_id,omitempty"`
	Capabilities []string   `json:"capabilities,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type Service struct {
	repo       ports.IdentityRepository
	identities *identity.Service
}

func NewService(db runtimeconfig.DBHandle) *Service {
	repo := sqlstore.NewIdentityRepository(db)
	return &Service{repo: repo, identities: identity.NewService(repo)}
}
func (s *Service) Identities() *identity.Service { return s.identities }

// The configured development key has a fixed ceiling but must still obey the
// current legacy Agent grant and workspace policy, including continuation checks.
func (s *Service) DevelopmentPrincipal(ctx context.Context, user string) (domain.AccessContext, error) {
	if _, err := s.identities.Personal(ctx, user); err != nil {
		return domain.AccessContext{}, err
	}
	detail, err := s.identities.Detail(ctx, user, domain.LegacyAgentID(user))
	if err != nil {
		return domain.AccessContext{}, err
	}
	if detail.Agent.Status != "active" || detail.Workspace.Status != "active" {
		return domain.AccessContext{}, domain.ErrAccessDenied
	}
	return domain.AccessContext{
		UserID: user, AgentID: detail.Agent.ID, CredentialID: "development", WorkspaceID: detail.Workspace.ID,
		Capabilities: detail.EffectiveCapabilities, PolicyVersion: detail.Workspace.PolicyVersion, GrantRevision: detail.Grant.Revision, Legacy: true,
	}, nil
}

func (s *Service) Create(ctx context.Context, user, name string) (Record, string, error) {
	return s.CreateWithOptions(ctx, user, CreateOptions{Name: name})
}

func (s *Service) CreateWithOptions(ctx context.Context, user string, input CreateOptions) (Record, string, error) {
	input.Name = strings.TrimSpace(input.Name)
	if user == "" || !identity.ValidateName(input.Name) || input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
		return Record{}, "", identity.ErrInvalidInput
	}
	if err := identity.ValidateCapabilities(input.Capabilities); err != nil {
		return Record{}, "", err
	}
	if input.AgentID != "" {
		if _, err := s.identities.Grant(ctx, user, input.AgentID); err != nil {
			return Record{}, "", err
		}
	}
	w, err := s.identities.Personal(ctx, user)
	if err != nil {
		return Record{}, "", err
	}
	if w.Status != "active" {
		return Record{}, "", domain.ErrAccessDenied
	}
	if input.AgentID == "" {
		input.AgentID = domain.LegacyAgentID(user)
	}
	grant, err := s.identities.Grant(ctx, user, input.AgentID)
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

func (s *Service) LookupUserByHash(ctx context.Context, prefix, hash string) (string, error) {
	a, err := s.LookupPrincipalByHash(ctx, prefix, hash)
	return a.UserID, err
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
			caps = domain.EffectiveCapabilities(k.Capabilities, d.EffectiveCapabilities, d.Workspace.Capabilities)
		}
		k.Hash = ""
		items = append(items, domain.CredentialAccess{Credential: k, EffectiveCapabilities: caps})
	}
	return items, nil
}
