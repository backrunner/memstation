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
	r.Get("/api/admin/workspaces/{workspaceID}/agents/{agentID}/detail", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		out, err := s.WorkspaceAgentDetail(r.Context(), r.URL.Query().Get("user_id"), chi.URLParam(r, "workspaceID"), chi.URLParam(r, "agentID"))
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	r.Post("/api/admin/workspaces/{workspaceID}/agents", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		var in struct {
			UserID       string   `json:"user_id"`
			Name         string   `json:"name"`
			Capabilities []string `json:"capabilities"`
		}
		if !decodeIdentity(w, r, &in) {
			return
		}
		out, err := s.CreateWorkspaceAgent(r.Context(), in.UserID, chi.URLParam(r, "workspaceID"), in.Name, in.Capabilities)
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 201, out)
	})

	r.Post("/api/admin/workspaces", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		var input domain.Workspace
		if !decodeIdentity(w, r, &input) {
			return
		}
		item, err := s.CreateWorkspace(r.Context(), input)
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 201, item)
	})
	r.Get("/api/admin/workspaces/{workspaceID}/integrations", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		items, err := s.Integrations(r.Context(), r.URL.Query().Get("user_id"), chi.URLParam(r, "workspaceID"))
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	})
	r.Put("/api/admin/workspaces/{workspaceID}/integrations", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		var input domain.IntegrationConfig
		if !decodeIdentity(w, r, &input) {
			return
		}
		input.WorkspaceID = chi.URLParam(r, "workspaceID")
		item, err := s.PutIntegration(r.Context(), input)
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, item)
	})
	r.Get("/api/admin/workspaces/{workspaceID}/partitions", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		items, err := s.Partitions(r.Context(), r.URL.Query().Get("user_id"), chi.URLParam(r, "workspaceID"))
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	})
	r.Put("/api/admin/workspaces/{workspaceID}/partitions", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		var input domain.MemoryPartition
		if !decodeIdentity(w, r, &input) {
			return
		}
		input.WorkspaceID = chi.URLParam(r, "workspaceID")
		item, err := s.PutPartition(r.Context(), input)
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, item)
	})
	r.Get("/api/admin/workspaces/{workspaceID}/agents/{agentID}/grant", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		item, err := s.WorkspaceGrant(r.Context(), r.URL.Query().Get("user_id"), chi.URLParam(r, "workspaceID"), chi.URLParam(r, "agentID"))
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, item)
	})
	r.Put("/api/admin/workspaces/{workspaceID}/agents/{agentID}/grant", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		var input struct {
			UserID       string   `json:"user_id"`
			Capabilities []string `json:"capabilities"`
			Revision     int64    `json:"expected_revision"`
		}
		if !decodeIdentity(w, r, &input) {
			return
		}
		item, err := s.SetGrant(r.Context(), domain.WorkspaceGrant{UserID: input.UserID, WorkspaceID: chi.URLParam(r, "workspaceID"), AgentID: chi.URLParam(r, "agentID"), Capabilities: input.Capabilities, Revision: input.Revision})
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, item)
	})

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
		writeJSON(w, 200, domain.WorkspacePage{Items: []domain.Workspace{item}, WorkspaceMode: "workspace"})
	}
}
