package httptransport

import (
	"net/http"
	"strconv"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	"memstation/apps/server/internal/service/identity"

	"github.com/go-chi/chi/v5"
)

func registerWorkspaceRoutes(r chi.Router, service func(http.ResponseWriter) *identity.Service) {
	r.Get("/api/admin/workspaces", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		limit := 0
		if value := r.URL.Query().Get("limit"); value != "" {
			var err error
			limit, err = strconv.Atoi(value)
			if err != nil {
				identityError(w, identity.ErrInvalidInput)
				return
			}
		}
		page, err := s.Workspaces(r.Context(), domain.WorkspaceQuery{UserID: r.URL.Query().Get("user_id"), After: r.URL.Query().Get("after"), Query: r.URL.Query().Get("q"), Limit: limit})
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, page)
	})
	r.Get("/api/admin/workspaces/{workspaceID}", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		item, err := s.Workspace(r.Context(), r.URL.Query().Get("user_id"), chi.URLParam(r, "workspaceID"))
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, item)
	})
	r.Put("/api/admin/workspaces/{workspaceID}", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		var input struct {
			UserID        string   `json:"user_id"`
			Name          string   `json:"name"`
			Capabilities  []string `json:"capabilities"`
			PolicyVersion int64    `json:"expected_policy_version"`
		}
		if !decodeIdentity(w, r, &input) {
			return
		}
		item, err := s.UpdateWorkspace(r.Context(), domain.Workspace{ID: chi.URLParam(r, "workspaceID"), UserID: input.UserID, Name: input.Name, Capabilities: input.Capabilities, PolicyVersion: input.PolicyVersion})
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, item)
	})
}

func workspaceDiscovery(keys *apikeyservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if keys == nil {
			writeError(w, 503, "unavailable", "identity service unavailable")
			return
		}
		access, _ := security.AccessFromContext(r.Context())
		item, err := keys.Identities().Workspace(r.Context(), access.UserID, access.WorkspaceID)
		if err != nil {
			identityError(w, err)
			return
		}
		// Discovery reveals only this credential's effective permissions, never
		// the broader workspace policy available to administrators.
		item.Capabilities = domain.EffectiveCapabilities(access.Capabilities, access.Capabilities, item.Capabilities)
		writeJSON(w, 200, domain.WorkspacePage{Items: []domain.Workspace{item}, WorkspaceMode: "personal_only"})
	}
}
