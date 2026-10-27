package testfixture

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
)

func FixtureAgentID(user string) string { return domain.AccessID("fixture-agent", user) }

func ProvisionFixtureAgent(t *testing.T, r ports.IdentityRepository, user string) string {
	t.Helper()
	ctx := context.Background()
	w, err := r.EnsurePersonal(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	id := FixtureAgentID(user)
	if _, err = r.GetAgent(ctx, user, id); err == nil {
		return id
	}
	a := domain.Agent{ID: id, UserID: user, ActorID: domain.AccessID("fixture-actor", user), Name: "Fixture agent", Status: "active", DefaultWorkspaceID: w.ID, Revision: 1}
	err = r.CreateAgent(ctx, a, domain.WorkspaceGrant{UserID: user, WorkspaceID: w.ID, AgentID: id, Capabilities: domain.PersonalCapabilities(), Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
