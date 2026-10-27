package security

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestAuthenticationRequiresRegisteredCurrentPrincipal(t *testing.T) {
	ctx := context.Background()
	if _, err := (Authenticator{}).AuthenticatePrincipal(ctx, "ms-local-dev"); err == nil {
		t.Fatal("unregistered convenience key authenticated")
	}
	good := domain.AccessContext{UserID: "owner", AgentID: "agent", CredentialID: "credential", WorkspaceID: domain.PersonalWorkspaceID("owner"), ScopeVersion: 2, PartitionIDs: []string{""}}
	for _, change := range []func(*domain.AccessContext){func(p *domain.AccessContext) { p.ScopeVersion = 0 }, func(p *domain.AccessContext) { p.ScopeVersion = 1 }, func(p *domain.AccessContext) { p.ScopeVersion = 3 }, func(p *domain.AccessContext) { p.AgentID = "" }, func(p *domain.AccessContext) { p.WorkspaceID = "" }, func(p *domain.AccessContext) { p.CredentialID = "" }} {
		p := good
		change(&p)
		auth := Authenticator{LookupPrincipal: func(context.Context, string, string) (domain.AccessContext, error) { return p, nil }}
		if _, err := auth.AuthenticatePrincipal(ctx, "ms-registered"); err == nil {
			t.Fatal("incomplete or unsupported principal authenticated", p)
		}
	}
	auth := Authenticator{LookupPrincipal: func(context.Context, string, string) (domain.AccessContext, error) { return good, nil }}
	if p, err := auth.AuthenticatePrincipal(ctx, "ms-registered"); err != nil || p.AgentID != good.AgentID {
		t.Fatal(p, err)
	}
}
