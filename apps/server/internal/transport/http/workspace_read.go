package httptransport

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/identity"
	"memstation/apps/server/internal/service/memory"
)

// These routes are mounted only inside the administrator session group. The
// URL workspace is authoritative; a query owner can narrow it, never replace it.
func registerWorkspaceReadRoutes(r chi.Router, deps Dependencies) {
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				if deps.APIKeys == nil || deps.Admin == nil || deps.Memory == nil {
					writeError(w, 503, "unavailable", "workspace browsing is unavailable")
					return
				}
				query := r.URL.Query()
				recordPage := strings.HasSuffix(r.URL.Path, "/conversations/page") || strings.HasSuffix(r.URL.Path, "/memory/keys/page")
				paged := recordPage || strings.HasSuffix(r.URL.Path, "/memory/documents/page")
				search := strings.HasSuffix(r.URL.Path, "/memory/search")
				for key, values := range query {
					allowed := key == "user_id" || key == "workspace_id" || (!recordPage && key == "generation_id") || (!search && ((!recordPage && key == "kind") || key == "limit")) || (paged && (key == "q" || key == "after"))
					if len(values) != 1 || !allowed {
						identityError(w, identity.ErrInvalidInput)
						return
					}
				}
				if value := query.Get("limit"); value != "" {
					limit, err := strconv.Atoi(value)
					if err != nil || limit < 1 || limit > 100 {
						identityError(w, identity.ErrInvalidInput)
						return
					}
				} else if !search {
					query.Set("limit", "100")
				}
				if strings.Contains(r.URL.Path, "/memory/keys") && query.Get("generation_id") != "" {
					memoryEditError(w, memory.ErrInvalidEdit)
					return
				}
				id := chi.URLParam(r, "workspaceID")
				if explicit := query.Get("workspace_id"); explicit != "" && explicit != id {
					identityError(w, domain.ErrAccessNotFound)
					return
				}
				ctx, workspace, err := deps.APIKeys.Identities().WorkspaceDataContext(r.Context(), query.Get("user_id"), id)
				if err != nil {
					identityError(w, err)
					return
				}
				r = r.Clone(ctx)
				query.Set("user_id", workspace.UserID)
				r.URL.RawQuery = query.Encode()
				next.ServeHTTP(w, r)
			})
		})
		registerWorkspaceEditRoutes(r, deps.Memory)
		registerWorkspaceSearchRoutes(r, deps.Memory)
		registerWorkspaceBrowseRoutes(r, deps)
		r.Get("/api/admin/workspaces/{workspaceID}/conversations", adminConversationsHandler(deps.Admin))
		r.Get("/api/admin/workspaces/{workspaceID}/conversations/{id}", adminConversationDetailHandler(deps.Admin))
		// Even an empty related collection must first prove conversation ownership.
		ownedConversation := func(next http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if _, err := deps.Admin.ConversationDetail(r.Context(), queryUserID(r), chi.URLParam(r, "id"), r.URL.Query().Get("generation_id")); err != nil {
					writeError(w, 404, "conversation_not_found", "conversation not found in this workspace")
					return
				}
				next(w, r)
			}
		}
		r.Get("/api/admin/workspaces/{workspaceID}/conversations/{id}/memory", ownedConversation(adminConversationMemoryHandler(deps.Admin, deps.Memory, deps.Generations)))
		r.Get("/api/admin/workspaces/{workspaceID}/conversations/{id}/sources", ownedConversation(adminConversationSourcesHandler(deps.Generations)))
		r.Get("/api/admin/workspaces/{workspaceID}/memory/documents", adminMemoryDocumentsHandler(deps.Memory))
		r.Get("/api/admin/workspaces/{workspaceID}/memory/documents/{kind}/{id}", adminMemoryDocumentGetHandler(deps.Memory))
	})
}
