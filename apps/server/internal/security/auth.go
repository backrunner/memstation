package security

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"memstation/apps/server/internal/domain"
)

type Authenticator struct {
	AdminToken                 string
	DevAPIKey                  string
	SessionManager             *AdminSessionManager
	LookupAPIKey               func(context.Context, string, string) (string, error)
	LookupPrincipal            func(context.Context, string, string) (domain.AccessContext, error)
	LookupDevelopmentPrincipal func(context.Context, string) (domain.AccessContext, error)
}

func (a Authenticator) AuthenticateAPIKey(ctx context.Context, r *http.Request) (string, error) {
	return a.AuthenticateAPIKeyToken(ctx, bearerToken(r.Header.Get("Authorization")))
}

func (a Authenticator) AuthenticateAPIKeyToken(ctx context.Context, token string) (string, error) {
	principal, err := a.AuthenticatePrincipal(ctx, token)
	return principal.UserID, err
}

func legacyPrincipal(user, credential string) domain.AccessContext {
	return domain.AccessContext{UserID: user, AgentID: domain.LegacyAgentID(user), CredentialID: credential, WorkspaceID: domain.PersonalWorkspaceID(user), Capabilities: domain.PersonalCapabilities(), PolicyVersion: 1, GrantRevision: 1, Legacy: true}
}

func (a Authenticator) AuthenticatePrincipal(ctx context.Context, token string) (domain.AccessContext, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return domain.AccessContext{}, errors.New("missing api key")
	}
	if a.DevAPIKey != "" && token == a.DevAPIKey {
		if a.LookupDevelopmentPrincipal != nil {
			return a.LookupDevelopmentPrincipal(ctx, DeriveUserIDFromAPIKey(token))
		}
		return legacyPrincipal(DeriveUserIDFromAPIKey(token), "development"), nil
	}
	if strings.HasPrefix(token, "ms-") {
		prefix, hash, err := (APIKeyHasher{}).Hash(token)
		if err != nil {
			return domain.AccessContext{}, errors.New("invalid api key")
		}
		if a.LookupPrincipal != nil {
			p, err := a.LookupPrincipal(ctx, prefix, hash)
			if err != nil || p.UserID == "" || p.AgentID == "" || p.CredentialID == "" || p.WorkspaceID != domain.PersonalWorkspaceID(p.UserID) {
				return domain.AccessContext{}, errors.New("invalid api key")
			}
			return p, nil
		}
		// Compatibility for embedded callers; production uses LookupPrincipal.
		if a.LookupAPIKey != nil {
			user, err := a.LookupAPIKey(ctx, prefix, hash)
			if err == nil && user != "" {
				return legacyPrincipal(user, prefix), nil
			}
		}
	}
	return domain.AccessContext{}, errors.New("invalid api key")
}

func (a Authenticator) AuthenticateContext(ctx context.Context, token, workspace string) (context.Context, error) {
	p, err := a.AuthenticatePrincipal(ctx, token)
	if err != nil {
		return ctx, err
	}
	if workspace != "" && workspace != p.WorkspaceID {
		return ctx, domain.ErrAccessDenied
	}
	ctx = WithAccess(ctx, p)
	// Re-check authority during tool continuations, including after an upstream call.
	return context.WithValue(ctx, accessRefreshKey, accessRefresh(func(ctx context.Context) (domain.AccessContext, error) {
		next, err := a.AuthenticatePrincipal(ctx, token)
		if err != nil || next.UserID != p.UserID || next.AgentID != p.AgentID || next.WorkspaceID != p.WorkspaceID {
			return domain.AccessContext{}, domain.ErrAccessDenied
		}
		return next, nil
	})), nil
}

func (a Authenticator) AuthenticateRequest(ctx context.Context, r *http.Request) (context.Context, error) {
	if len(r.Header.Values("X-MemStation-Workspace")) > 1 {
		return ctx, domain.ErrAccessDenied
	}
	return a.AuthenticateContext(ctx, bearerToken(r.Header.Get("Authorization")), strings.TrimSpace(r.Header.Get("X-MemStation-Workspace")))
}

func (a Authenticator) AuthenticateAdmin(r *http.Request) bool {
	return a.AuthenticateAdminSession(r)
}

func (a Authenticator) AuthenticateAdminSession(r *http.Request) bool {
	cookie, err := r.Cookie(AdminSessionCookieName)
	return err == nil && a.SessionManager != nil && a.SessionManager.Verify(cookie.Value)
}

func (a Authenticator) AuthenticateAdminToken(token string) bool {
	return VerifyAdminToken(a.AdminToken, token)
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}
