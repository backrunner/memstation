package worker

import (
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestGenerationIdentityPreservesAlreadyScopedRelationEndpoints(t *testing.T) {
	for _, gen := range []string{("gen:current:" + domain.PersonalWorkspaceID("owner")), ("gen:current:" + domain.PersonalWorkspaceID("group")), "rebuilt"} {
		once := scopeStableID("owner", gen, "node:summary:conversation:v1")
		if twice := scopeStableID("owner", gen, once); twice != once {
			t.Fatalf("generation %s duplicated endpoint %s", gen, twice)
		}
	}
}
