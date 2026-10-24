package httptransport

import (
	"net/http"
	"strconv"

	"memstation/apps/server/internal/domain"

	"github.com/go-chi/chi/v5"
)

func registerWorkspaceBrowseRoutes(r chi.Router, deps Dependencies) {
	input := func(r *http.Request) domain.RecordBrowseQuery {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		return domain.RecordBrowseQuery{UserID: queryUserID(r), Query: q.Get("q"), After: q.Get("after"), Limit: limit}
	}
	r.Get("/api/admin/workspaces/{workspaceID}/conversations/page", func(w http.ResponseWriter, r *http.Request) {
		result, err := deps.Admin.BrowseConversations(r.Context(), input(r))
		if err != nil {
			workspaceSearchError(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	r.Get("/api/admin/workspaces/{workspaceID}/memory/keys/page", func(w http.ResponseWriter, r *http.Request) {
		result, err := deps.Memory.BrowseKeyMemories(r.Context(), input(r))
		if err != nil {
			workspaceSearchError(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
}
