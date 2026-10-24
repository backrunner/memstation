package apikey

import (
	"context"
	"testing"

	"memstation/apps/server/internal/security"
)

func TestDevelopmentKeyHonorsLiveWorkspaceAndAgent(t *testing.T) {
	ctx := context.Background()
	s := NewService(nil)
	auth := security.Authenticator{DevAPIKey: "workspace-dev-test", LookupDevelopmentPrincipal: s.DevelopmentPrincipal}
	access, err := auth.AuthenticatePrincipal(ctx, "workspace-dev-test")
	if err != nil || access.CredentialID != "development" || len(access.Capabilities) == 0 {
		t.Fatal(access, err)
	}
	w, err := s.Identities().Workspace(ctx, access.UserID, access.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	w.Capabilities = []string{}
	if _, err = s.Identities().UpdateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	access, err = auth.AuthenticatePrincipal(ctx, "workspace-dev-test")
	if err != nil || len(access.Capabilities) != 0 || access.PolicyVersion != 2 {
		t.Fatal("development key bypassed policy", access, err)
	}
	detail, err := s.Identities().Detail(ctx, access.UserID, access.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	detail.Agent.Status = "disabled"
	if _, err = s.Identities().Update(ctx, detail.Agent); err != nil {
		t.Fatal(err)
	}
	if _, err = auth.AuthenticatePrincipal(ctx, "workspace-dev-test"); err == nil {
		t.Fatal("development key bypassed disabled Agent")
	}
}
