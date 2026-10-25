package security

import (
	"context"
	"errors"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestBoundWorkspaceScopeNeverWidens(t *testing.T) {
	base := context.Background()
	if _, err := DataScope(base, "owner", "group"); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("unbound workspace accepted", err)
	}
	unverified := WithAccess(base, domain.AccessContext{UserID: "owner", WorkspaceID: "group"})
	if _, err := DataScope(unverified, "owner", ""); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("unverified scope accepted", err)
	}
	ctx, err := WithDataScope(base, "owner", "group", "private")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DataScope(ctx, "owner", ""); err != nil || got != "group" {
		t.Fatal(got, err)
	}
	for _, input := range [][2]string{{"other", "group"}, {"owner", "another"}, {"owner", domain.PersonalWorkspaceID("owner")}} {
		if _, err := DataScope(ctx, input[0], input[1]); !errors.Is(err, domain.ErrAccessDenied) {
			t.Fatal(input, err)
		}
	}
	parts := Partitions(ctx)
	parts[0] = ""
	if CanReadPartition(ctx, "") || !CanReadPartition(ctx, "private") {
		t.Fatal("partition authority mutated")
	}
	if _, err := WithDataScope(ctx, "owner", "another"); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("nested scope widened")
	}
}

func TestScopedIdentityKeepsLegacyAndSeparatesWorkspaces(t *testing.T) {
	base := context.Background()
	if ScopedIdentity(base, "owner") != "owner" {
		t.Fatal("legacy identity changed")
	}
	a, _ := WithDataScope(base, "owner", "group-a")
	b, _ := WithDataScope(base, "owner", "group-b")
	private, _ := WithDataScope(base, "owner", "group-a", "private")
	seen := map[string]bool{}
	for _, ctx := range []context.Context{base, a, b, private} {
		id := ScopedIdentity(ctx, "owner")
		if seen[id] {
			t.Fatal("scope identity collision")
		}
		seen[id] = true
	}
}
