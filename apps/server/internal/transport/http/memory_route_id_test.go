package httptransport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMemoryRouteIDDecodesExactlyOnce(t *testing.T) {
	for _, test := range []struct{ encoded, want string }{
		{"gen%3Acurrent%3Anode%3Afact", "gen:current:node:fact"},
		{"literal%252Fidentifier", "literal%2Fidentifier"},
		{"plain-node", "plain-node"},
	} {
		t.Run(test.encoded, func(t *testing.T) {
			router := chi.NewRouter()
			router.Get("/records/{id}", func(w http.ResponseWriter, r *http.Request) {
				if got := memoryRouteID(r, "id"); got != test.want {
					t.Fatalf("route ID: got %q, want %q", got, test.want)
				}
				w.WriteHeader(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/records/"+test.encoded, nil))
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("route did not match: %d", recorder.Code)
			}
		})
	}
}

func TestMemoryKindRejectsRetiredAliases(t *testing.T) {
	for _, value := range []string{"summaries", "facts", "segments", "keymemories", "key_memories", "key-memory", "agent_actions", "agent-action", "agent-actions"} {
		if _, err := parseMemoryKind(value); err == nil {
			t.Fatal("memory kind alias accepted", value)
		}
	}
	for _, value := range []string{"summary", "segment", "fact", "key_memory", "agent_action"} {
		if _, err := parseMemoryKind(value); err != nil {
			t.Fatal("canonical kind rejected", value, err)
		}
	}
}
