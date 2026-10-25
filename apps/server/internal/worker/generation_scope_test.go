package worker

import "testing"

func TestGenerationIdentityPreservesAlreadyScopedRelationEndpoints(t *testing.T) {
	for _, gen := range []string{"gen:current:owner", "gen:current:group", "rebuilt"} {
		once := scopeStableID("owner", gen, "node:summary:conversation:v1")
		if twice := scopeStableID("owner", gen, once); twice != once {
			t.Fatalf("generation %s duplicated endpoint %s", gen, twice)
		}
	}
}
