package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	"memstation/apps/server/internal/service/identity"

	"github.com/go-chi/chi/v5"
)

func proxyCapability(r *http.Request) string {
	switch {
	case r.URL.Path == "/api/v1/identity", r.URL.Path == "/api/v1/workspaces":
		return ""
	case strings.HasPrefix(r.URL.Path, "/v1/files"):
		if r.Method == http.MethodGet {
			return domain.CapabilityFilesRead
		}
		return domain.CapabilityFilesWrite
	case strings.HasPrefix(r.URL.Path, "/api/v1/memory"):
		if r.Method == http.MethodPut || r.Method == http.MethodDelete {
			return domain.CapabilityMemoryEdit
		}
		return domain.CapabilityMemoryRead
	default:
		return domain.CapabilityGateway
	}
}

func identityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidInput):
		writeError(w, 400, "invalid_request", err.Error())
	case errors.Is(err, domain.ErrAccessNotFound):
		writeError(w, 404, "not_found", "identity not found")
	case errors.Is(err, domain.ErrAccessConflict):
		writeError(w, 409, "revision_conflict", "identity changed; reload before saving")
	case errors.Is(err, domain.ErrAccessDenied):
		writeError(w, 403, "access_denied", "identity access denied")
	default:
		writeError(w, 500, "identity_failed", "identity operation failed")
	}
}

func decodeIdentity(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, 400, "invalid_request", "invalid identity payload")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, 400, "invalid_request", "expected one JSON object")
		return false
	}
	return true
}

func registerIdentityRoutes(r chi.Router, keys *apikeyservice.Service) {
	service := func(w http.ResponseWriter) *identity.Service {
		w.Header().Set("Cache-Control", "no-store")
		if keys == nil {
			writeError(w, 503, "unavailable", "identity service unavailable")
			return nil
		}
		return keys.Identities()
	}
	registerWorkspaceRoutes(r, service)
	r.Get("/api/admin/users/{id}/agents", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 100 {
			limit = 50
		}
		items, err := s.Search(r.Context(), domain.AgentQuery{UserID: chi.URLParam(r, "id"), After: r.URL.Query().Get("after"), Query: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status"), Limit: limit})
		if err != nil {
			identityError(w, err)
			return
		}
		next := ""
		if len(items) == limit {
			next = items[len(items)-1].ID
		}
		writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next, "workspace_mode": "personal_only"})
	})

	r.Get("/api/admin/users/{id}/agents/{agentID}", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		detail, err := s.Detail(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "agentID"))
		if err != nil {
			identityError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, detail)
	})
	r.Get("/api/admin/users/{id}/agents/{agentID}/api-keys", func(w http.ResponseWriter, r *http.Request) {
		if service(w) == nil {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 100 {
			limit = 50
		}
		items, err := keys.ListForAgent(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "agentID"), r.URL.Query().Get("after"), limit)
		if err != nil {
			identityError(w, err)
			return
		}
		next := ""
		if len(items) == limit {
			next = items[len(items)-1].ID
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
	})
	r.Post("/api/admin/users/{id}/agents", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		var input struct {
			Name         string   `json:"name"`
			Capabilities []string `json:"capabilities"`
		}
		if !decodeIdentity(w, r, &input) {
			return
		}
		item, err := s.Create(r.Context(), chi.URLParam(r, "id"), input.Name, input.Capabilities)
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 201, item)
	})
	r.Put("/api/admin/users/{id}/agents/{agentID}", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		var input struct {
			Name     string `json:"name"`
			Status   string `json:"status"`
			Revision int64  `json:"expected_revision"`
		}
		if !decodeIdentity(w, r, &input) {
			return
		}
		item, err := s.Update(r.Context(), domain.Agent{ID: chi.URLParam(r, "agentID"), UserID: chi.URLParam(r, "id"), Name: input.Name, Status: input.Status, Revision: input.Revision})
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, item)
	})
	r.Get("/api/admin/users/{id}/agents/{agentID}/grant", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		g, err := s.Grant(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "agentID"))
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, g)
	})
	r.Put("/api/admin/users/{id}/agents/{agentID}/grant", func(w http.ResponseWriter, r *http.Request) {
		s := service(w)
		if s == nil {
			return
		}
		var input struct {
			Capabilities []string `json:"capabilities"`
			Revision     int64    `json:"expected_revision"`
		}
		if !decodeIdentity(w, r, &input) {
			return
		}
		user := chi.URLParam(r, "id")
		g, err := s.SetGrant(r.Context(), domain.WorkspaceGrant{UserID: user, AgentID: chi.URLParam(r, "agentID"), WorkspaceID: domain.PersonalWorkspaceID(user), Capabilities: input.Capabilities, Revision: input.Revision})
		if err != nil {
			identityError(w, err)
			return
		}
		writeJSON(w, 200, g)
	})
}

func identityHandler(w http.ResponseWriter, r *http.Request) {
	access, _ := security.AccessFromContext(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"access": access, "workspace_mode": "personal_only", "private_memory": false, "event_ingestion": false})
}

func writeGatewayError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrAccessDenied) || errors.Is(err, domain.ErrAccessConflict) {
		identityError(w, err)
		return
	}
	writeError(w, http.StatusBadGateway, "provider_error", err.Error())
}
