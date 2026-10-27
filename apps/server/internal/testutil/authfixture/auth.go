package authfixture

import (
	"context"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

const Token = "ms-test-registered-credential"

func LookupPrincipal(_ context.Context, prefix, hash string) (domain.AccessContext, error) {
	wantPrefix, wantHash, err := (security.APIKeyHasher{}).Hash(Token)
	if err != nil || prefix != wantPrefix || hash != wantHash {
		return domain.AccessContext{}, domain.ErrAccessDenied
	}
	user := security.DeriveUserIDFromAPIKey(Token)
	return domain.AccessContext{UserID: user, AgentID: "test-agent", CredentialID: "test-credential", WorkspaceID: domain.PersonalWorkspaceID(user), ScopeVersion: 2, PartitionIDs: []string{""}, Audience: domain.Audience{Kind: "workspace"}, Capabilities: domain.PersonalCapabilities(), PolicyVersion: 1, GrantRevision: 1}, nil
}
