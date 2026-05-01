package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/hibiken/asynq"

	"memstation/apps/server/graph/resolver"
	"memstation/apps/server/internal/bootstrap"
	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	"memstation/apps/server/internal/observability"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/admin"
	apikeyservice "memstation/apps/server/internal/service/apikey"
	auditsvc "memstation/apps/server/internal/service/audit"
	connectionconfigservice "memstation/apps/server/internal/service/connectionconfig"
	gatewayservice "memstation/apps/server/internal/service/gateway"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	multimodalservice "memstation/apps/server/internal/service/multimodal"
	providerconfigservice "memstation/apps/server/internal/service/providerconfig"
	settingsservice "memstation/apps/server/internal/service/settings"
	systemconfigservice "memstation/apps/server/internal/service/systemconfig"
	usageservice "memstation/apps/server/internal/service/usage"
	userservice "memstation/apps/server/internal/service/user"
	"memstation/apps/server/internal/transport/graphql"
	"memstation/apps/server/internal/transport/http/protocol"
	"memstation/apps/server/internal/worker"
)

type Dependencies struct {
	Config           bootstrap.Config
	Logger           *slog.Logger
	Health           *observability.Registry
	Auth             security.Authenticator
	RateLimiter      *security.RateLimiter
	Session          *security.AdminSessionManager
	Models           *modelcap.Registry
	GraphQL          *resolver.Resolver
	Gateway          *gatewayservice.Service
	Multimodal       *multimodalservice.Service
	Memory           *memoryservice.Service
	Generations      *memorygenservice.Service
	Admin            *admin.Service
	Audit            *auditsvc.Service
	Usage            *usageservice.Service
	Users            *userservice.Service
	APIKeys          *apikeyservice.Service
	Settings         *settingsservice.Service
	ConnectionConfig *connectionconfigservice.Service
	SystemConfig     *systemconfigservice.Service
	ProviderConfig   *providerconfigservice.Service
	Publisher        ports.TaskPublisher
	Inspector        *asynq.Inspector
}

func NewRouter(deps Dependencies) http.Handler {
	router := chi.NewRouter()
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	router.Use(chimiddleware.RequestID)
	router.Use(chimiddleware.RealIP)
	router.Use(corsMiddleware(deps.Config, deps.SystemConfig))
	router.Use(observability.HTTPMiddleware())
	router.Use(chimiddleware.Recoverer)
	router.Use(requestLogger(logger))

	router.Get("/health/live", liveHandler(deps.Config))
	router.Get("/health/ready", readyHandler(deps.Config, deps.Health))
	adminOrigin := adminOriginMiddleware(deps.Config, deps.SystemConfig)
	router.With(adminOrigin).Post("/api/admin/login", adminLoginHandler(deps.Config, deps.SystemConfig, deps.Auth, deps.Session, deps.Audit))
	router.With(adminOrigin).Post("/api/admin/logout", adminLogoutHandler(deps.Config, deps.SystemConfig, deps.Auth, deps.Audit))
	router.With(adminOrigin).Get("/api/admin/session", adminSessionHandler(deps.Auth))

	router.Group(func(r chi.Router) {
		r.Use(proxyAuthMiddleware(deps.Auth, deps.RateLimiter))
		r.Get("/v1/models", modelsHandler(deps.Models))
		r.Post("/v1/chat/completions", chatHandler(deps.Gateway))
		r.Post("/v1/responses", responsesHandler(deps.Gateway))
		r.Post("/v1/messages", anthropicHandler(deps.Gateway))
		r.Post("/v1/files", uploadFileHandler(deps.Multimodal))
		r.Get("/v1/files/{id}", getFileHandler(deps.Multimodal, false))
		r.Delete("/v1/files/{id}", deleteFileHandler(deps.Multimodal))
		r.Post("/api/v1/memory/search", memorySearchHandler(deps.Memory))
		r.Get("/api/v1/memory/keys", keyMemoryListHandler(deps.Memory))
		r.Put("/api/v1/memory/keys/{key}", keyMemoryUpsertHandler(deps.Memory, deps.Audit))
		r.Delete("/api/v1/memory/keys/{key}", keyMemoryDeleteHandler(deps.Memory, deps.Audit))
	})

	router.Group(func(r chi.Router) {
		r.Use(adminOrigin)
		r.Use(adminAuthMiddleware(deps.Auth))
		r.Method(http.MethodGet, "/graphql/playground", graphql.Playground())
		r.Method(http.MethodPost, "/graphql", graphql.Handler(deps.GraphQL))
		r.Get("/api/admin/overview", adminOverviewHandler(deps.Admin))
		r.Get("/api/admin/users", adminUsersHandler(deps.Admin))
		r.Get("/api/admin/users/{id}", adminUserDetailHandler(deps.Admin))
		r.Get("/api/admin/users/{id}/sources", adminUserSourcesHandler(deps.Generations))
		r.Put("/api/admin/users/{id}/settings", adminUserSettingsHandler(deps.Settings, deps.Publisher, deps.Audit))
		r.Post("/api/admin/users/{id}/api-keys", adminUserAPIKeyCreateHandler(deps.APIKeys, deps.Audit))
		r.Delete("/api/admin/users/{id}/api-keys/{keyID}", adminUserAPIKeyRevokeHandler(deps.APIKeys, deps.Audit))
		r.Get("/api/admin/conversations", adminConversationsHandler(deps.Admin))
		r.Get("/api/admin/conversations/{id}", adminConversationDetailHandler(deps.Admin))
		r.Get("/api/admin/files/{id}", getFileHandler(deps.Multimodal, true))
		r.Get("/api/admin/conversations/{id}/memory", adminConversationMemoryHandler(deps.Admin, deps.Memory, deps.Generations))
		r.Get("/api/admin/conversations/{id}/sources", adminConversationSourcesHandler(deps.Generations))
		r.Get("/api/admin/memory/documents", adminMemoryDocumentsHandler(deps.Memory))
		r.Get("/api/admin/memory/documents/{kind}/{id}", adminMemoryDocumentGetHandler(deps.Memory))
		r.Put("/api/admin/memory/documents/{kind}/{id}", adminMemoryDocumentUpdateHandler(deps.Memory, deps.Audit))
		r.Post("/api/admin/memory/search", adminMemorySearchHandler(deps.Memory))
		r.Get("/api/admin/memory/rebuilds", adminMemoryRebuildListHandler(deps.Generations))
		r.Post("/api/admin/memory/rebuilds", adminMemoryRebuildCreateHandler(deps.Generations, deps.Publisher, deps.Audit))
		r.Get("/api/admin/memory/rebuilds/{id}", adminMemoryRebuildGetHandler(deps.Generations))
		r.Post("/api/admin/memory/rebuilds/{id}/activate", adminMemoryRebuildActivateHandler(deps.Generations, deps.Audit))
		r.Get("/api/admin/memory/keys", adminMemoryKeysHandler(deps.Memory))
		r.Post("/api/admin/memory/keys", adminMemoryKeyCreateHandler(deps.Memory, deps.Audit))
		r.Get("/api/admin/memory/keys/{id}", adminMemoryKeyGetHandler(deps.Memory))
		r.Put("/api/admin/memory/keys/{id}", adminMemoryKeyUpdateHandler(deps.Memory, deps.Audit))
		r.Delete("/api/admin/memory/keys/{id}", adminMemoryKeyDeleteHandler(deps.Memory, deps.Audit))
		r.Post("/api/admin/playground/memory-preview", adminMemoryPreviewHandler(deps.Admin))
		r.Get("/api/admin/stats/tokens", adminTokenStatsHandler(deps.Usage))
		r.Get("/api/admin/config", adminConfigGetHandler(deps.Config, deps.Settings, deps.ConnectionConfig, deps.SystemConfig, deps.ProviderConfig))
		r.Put("/api/admin/config", adminConfigPutHandler(deps.Config, deps.Settings, deps.ConnectionConfig, deps.SystemConfig, deps.ProviderConfig, deps.Publisher, deps.Audit))
		r.Get("/api/admin/providers/diagnostics", adminProviderDiagnosticsHandler(deps.Config, deps.SystemConfig, deps.ProviderConfig))
		r.Get("/api/admin/queues", adminQueuesHandler(deps.Inspector))
		r.Get("/api/admin/tasks", adminTasksHandler(deps.Inspector))
		r.Get("/api/admin/tasks/detail", adminTaskDetailHandler(deps.Inspector))
		r.Get("/api/admin/cache", adminCacheHandler(deps.Config, deps.SystemConfig, deps.Health))
		r.Post("/api/admin/tasks/requeue", adminRequeueHandler(deps.Publisher, deps.Inspector, deps.Audit))
		r.Get("/api/admin/audit", adminAuditHandler(deps.Audit))
	})

	return router
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			logger.InfoContext(r.Context(), "http request completed", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
		})
	}
}

func proxyAuthMiddleware(auth security.Authenticator, limiter *security.RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, err := auth.AuthenticateAPIKey(r.Context(), r)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
				return
			}
			if limiter != nil && !limiter.Allow(security.RateLimitKey(userID, r)) {
				writeError(w, http.StatusTooManyRequests, "rate_limited", "proxy rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r.WithContext(security.WithUserID(r.Context(), userID)))
		})
	}
}

func adminAuthMiddleware(auth security.Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !auth.AuthenticateAdmin(r) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "admin authentication required")
				return
			}
			next.ServeHTTP(w, r.WithContext(security.WithAdmin(r.Context(), true)))
		})
	}
}

func adminOriginMiddleware(cfg bootstrap.Config, systemConfig *systemconfigservice.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" && !currentCORSOriginPolicy(cfg, systemConfig).Allows(origin) {
				writeError(w, http.StatusForbidden, "forbidden_origin", "admin request origin is not allowed")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func adminLoginHandler(cfg bootstrap.Config, systemConfig *systemconfigservice.Service, auth security.Authenticator, session *security.AdminSessionManager, audit *auditsvc.Service) http.HandlerFunc {
	type requestBody struct {
		Token string `json:"token"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := readAdminToken(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if token == "" {
			var body requestBody
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				token = body.Token
			}
		}
		if !auth.AuthenticateAdminToken(token) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid admin token")
			return
		}
		if session == nil {
			writeError(w, http.StatusInternalServerError, "session_unavailable", "admin session manager is not configured")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     security.AdminSessionCookieName,
			Value:    session.Issue("admin"),
			Path:     "/",
			HttpOnly: true,
			SameSite: sessionSameSite(cfg, systemConfig),
			Secure:   cfg.SessionCookieSecure,
			MaxAge:   session.MaxAge(),
		})
		if audit != nil {
			audit.Record(r.Context(), domain.AuditEvent{
				ActorType:  "admin",
				ActorID:    "admin",
				Action:     "admin_login",
				TargetType: "session",
				TargetID:   "admin",
				CreatedAt:  time.Now().UTC(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
	}
}

func adminLogoutHandler(cfg bootstrap.Config, systemConfig *systemconfigservice.Service, auth security.Authenticator, audit *auditsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if audit != nil && auth.AuthenticateAdmin(r) {
			audit.Record(r.Context(), domain.AuditEvent{
				ActorType:  "admin",
				ActorID:    "admin",
				Action:     "admin_logout",
				TargetType: "session",
				TargetID:   "admin",
				CreatedAt:  time.Now().UTC(),
			})
		}
		http.SetCookie(w, &http.Cookie{
			Name:     security.AdminSessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: sessionSameSite(cfg, systemConfig),
			Secure:   cfg.SessionCookieSecure,
			MaxAge:   -1,
		})
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
	}
}

func adminSessionHandler(auth security.Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": auth.AuthenticateAdmin(r)})
	}
}

func liveHandler(cfg bootstrap.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "live",
			"version": cfg.Version,
			"mode":    "gateway",
		})
	}
}

func readyHandler(cfg bootstrap.Config, health *observability.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		components, err := health.Run(r.Context())
		status := "ready"
		code := http.StatusOK
		if err != nil {
			status = "degraded"
			code = http.StatusServiceUnavailable
		}
		services := map[string]string{}
		for _, component := range components {
			services[component.Name] = component.Status
		}
		writeJSON(w, code, map[string]any{
			"status":   status,
			"version":  cfg.Version,
			"mode":     "gateway",
			"services": services,
		})
	}
}

func modelsHandler(registry *modelcap.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": registry.List()})
	}
}

func chatHandler(service *gatewayservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request protocol.OpenAIChatCompletionsRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		unified := protocol.NormalizeChatCompletion(request, protocol.ParseHeaderOptions(r))
		unified.UserID = security.UserIDFromContext(r.Context())
		if service != nil {
			resolved, err := service.ResolveRequestDefaults(r.Context(), unified)
			if err == nil {
				unified = resolved
			}
		}
		setGatewayResponseHeaders(w, unified)
		if request.Stream {
			streamWriter, err := protocol.NewChatCompletionStreamWriter(w)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "stream_error", err.Error())
				return
			}
			response, _, err := service.ProxyStream(r.Context(), unified, streamWriter.Event)
			if err != nil {
				if streamWriter.Started() {
					_ = streamWriter.Error(err)
					return
				}
				writeError(w, http.StatusBadGateway, "provider_error", err.Error())
				return
			}
			if err := streamWriter.Close(*response); err != nil {
				writeError(w, http.StatusInternalServerError, "stream_error", err.Error())
			}
			return
		}
		response, _, err := service.Proxy(r.Context(), unified)
		if err != nil {
			writeError(w, http.StatusBadGateway, "provider_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, protocol.FormatChatCompletion(*response))
	}
}

func responsesHandler(service *gatewayservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request protocol.OpenAIResponsesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		unified := protocol.NormalizeResponses(request, protocol.ParseHeaderOptions(r))
		unified.UserID = security.UserIDFromContext(r.Context())
		if service != nil {
			resolved, err := service.ResolveRequestDefaults(r.Context(), unified)
			if err == nil {
				unified = resolved
			}
		}
		setGatewayResponseHeaders(w, unified)
		if request.Stream {
			streamWriter, err := protocol.NewResponsesStreamWriter(w)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "stream_error", err.Error())
				return
			}
			response, _, err := service.ProxyStream(r.Context(), unified, streamWriter.Event)
			if err != nil {
				if streamWriter.Started() {
					_ = streamWriter.Error(err)
					return
				}
				writeError(w, http.StatusBadGateway, "provider_error", err.Error())
				return
			}
			if err := streamWriter.Close(*response); err != nil {
				writeError(w, http.StatusInternalServerError, "stream_error", err.Error())
			}
			return
		}
		response, _, err := service.Proxy(r.Context(), unified)
		if err != nil {
			writeError(w, http.StatusBadGateway, "provider_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, protocol.FormatResponses(*response))
	}
}

func anthropicHandler(service *gatewayservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request protocol.AnthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		unified := protocol.NormalizeAnthropic(request, protocol.ParseHeaderOptions(r))
		unified.UserID = security.UserIDFromContext(r.Context())
		if service != nil {
			resolved, err := service.ResolveRequestDefaults(r.Context(), unified)
			if err == nil {
				unified = resolved
			}
		}
		setGatewayResponseHeaders(w, unified)
		if request.Stream {
			streamWriter, err := protocol.NewAnthropicStreamWriter(w)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "stream_error", err.Error())
				return
			}
			response, _, err := service.ProxyStream(r.Context(), unified, streamWriter.Event)
			if err != nil {
				if streamWriter.Started() {
					_ = streamWriter.Error(err)
					return
				}
				writeError(w, http.StatusBadGateway, "provider_error", err.Error())
				return
			}
			if err := streamWriter.Close(*response); err != nil {
				writeError(w, http.StatusInternalServerError, "stream_error", err.Error())
			}
			return
		}
		response, _, err := service.Proxy(r.Context(), unified)
		if err != nil {
			writeError(w, http.StatusBadGateway, "provider_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, protocol.FormatAnthropic(*response))
	}
}

func uploadFileHandler(service *multimodalservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "multimodal_unavailable", "multimodal storage is not configured")
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "multipart form field \"file\" is required")
			return
		}
		defer file.Close()

		metadata := map[string]any{}
		if raw := strings.TrimSpace(r.FormValue("metadata")); raw != "" {
			if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", "metadata must be valid JSON")
				return
			}
		}
		blob, err := service.SaveUpload(
			r.Context(),
			security.UserIDFromContext(r.Context()),
			header.Filename,
			firstNonEmptyString(header.Header.Get("Content-Type"), r.FormValue("mime_type")),
			file,
			metadata,
		)
		if err != nil {
			writeError(w, http.StatusBadRequest, "upload_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"blob_id":           blob.BlobID,
			"user_id":           blob.UserID,
			"mime_type":         blob.MIMEType,
			"size_bytes":        blob.SizeBytes,
			"sha256":            blob.SHA256,
			"original_filename": blob.OriginalFilename,
			"source_kind":       blob.SourceKind,
			"source_url":        "/v1/files/" + blob.BlobID,
			"metadata":          blob.Metadata,
			"created_at":        blob.CreatedAt,
		})
	}
}

func getFileHandler(service *multimodalservice.Service, adminView bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "multimodal_unavailable", "multimodal storage is not configured")
			return
		}
		userID := security.UserIDFromContext(r.Context())
		if adminView {
			userID = queryUserID(r)
		}
		blob, reader, err := service.OpenBlob(r.Context(), userID, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "blob_not_found", err.Error())
			return
		}
		defer reader.Close()
		if blob.MIMEType != "" {
			w.Header().Set("Content-Type", blob.MIMEType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		if blob.OriginalFilename != "" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", blob.OriginalFilename))
		}
		if _, err := io.Copy(w, reader); err != nil {
			writeError(w, http.StatusInternalServerError, "blob_read_failed", err.Error())
		}
	}
}

func deleteFileHandler(service *multimodalservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "multimodal_unavailable", "multimodal storage is not configured")
			return
		}
		userID := security.UserIDFromContext(r.Context())
		if err := service.DeleteBlob(r.Context(), userID, chi.URLParam(r, "id")); err != nil {
			writeError(w, http.StatusNotFound, "blob_not_found", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func memorySearchHandler(service *memoryservice.Service) http.HandlerFunc {
	type requestBody struct {
		Query               string              `json:"query"`
		TopN                int                 `json:"top_n"`
		IncludeConversation bool                `json:"include_conversation"`
		IncludeSegments     bool                `json:"include_segments"`
		MemoryTypes         []domain.MemoryType `json:"memory_types"`
		TimeTiers           []string            `json:"time_tiers"`
		DateFrom            string              `json:"date_from"`
		DateTo              string              `json:"date_to"`
		MinScore            float64             `json:"min_score"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		dateFrom, err := parseOptionalRFC3339(body.DateFrom)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		dateTo, err := parseOptionalRFC3339(body.DateTo)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		timeTiers, err := parseTimeTierList(body.TimeTiers)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result, err := service.Search(r.Context(), domain.SearchInput{
			UserID:              security.UserIDFromContext(r.Context()),
			Query:               body.Query,
			TopN:                body.TopN,
			IncludeConversation: body.IncludeConversation,
			IncludeSegments:     body.IncludeSegments,
			MemoryTypes:         body.MemoryTypes,
			TimeTiers:           timeTiers,
			DateFrom:            dateFrom,
			DateTo:              dateTo,
			MinScore:            body.MinScore,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "search_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func keyMemoryListHandler(service *memoryservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		topK, _ := strconv.Atoi(r.URL.Query().Get("top_k"))
		dateFrom, err := parseOptionalRFC3339(r.URL.Query().Get("date_from"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		dateTo, err := parseOptionalRFC3339(r.URL.Query().Get("date_to"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		timeTiers, err := parseTimeTierList(r.URL.Query()["time_tier"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		memories, err := service.SearchKeyMemories(r.Context(), domain.KeyMemoryQuery{
			UserID:    security.UserIDFromContext(r.Context()),
			Query:     r.URL.Query().Get("query"),
			TopK:      topK,
			DateFrom:  dateFrom,
			DateTo:    dateTo,
			TimeTiers: timeTiers,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "key_memory_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": memories})
	}
}

func keyMemoryUpsertHandler(service *memoryservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	type requestBody struct {
		Value        string `json:"value"`
		Category     string `json:"category"`
		Source       string `json:"source"`
		ReviewStatus string `json:"review_status"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		key := chi.URLParam(r, "key")
		userID := security.UserIDFromContext(r.Context())
		action := "create_key_memory"
		if service != nil {
			items, err := service.ListKeyMemories(r.Context(), userID, 200)
			if err == nil {
				for _, item := range items {
					if item.Key == key {
						action = "update_key_memory"
						break
					}
				}
			}
		}
		item, err := service.UpsertKeyMemory(r.Context(), domain.KeyMemory{
			UserID:       userID,
			Key:          key,
			Value:        body.Value,
			Category:     body.Category,
			Source:       defaultString(body.Source, "rest"),
			ReviewStatus: body.ReviewStatus,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "key_memory_failed", err.Error())
			return
		}
		if audit != nil {
			audit.Record(r.Context(), domain.AuditEvent{
				ActorType:  "user",
				ActorID:    userID,
				Action:     action,
				TargetType: "key_memory",
				TargetID:   item.ID,
				Payload: map[string]any{
					"key":      item.Key,
					"category": item.Category,
				},
				CreatedAt: time.Now().UTC(),
			})
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func keyMemoryDeleteHandler(service *memoryservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := security.UserIDFromContext(r.Context())
		key := chi.URLParam(r, "key")
		if err := service.DeleteKeyMemory(r.Context(), userID, key); err != nil {
			writeError(w, http.StatusInternalServerError, "key_memory_failed", err.Error())
			return
		}
		if audit != nil {
			audit.Record(r.Context(), domain.AuditEvent{
				ActorType:  "user",
				ActorID:    userID,
				Action:     "delete_key_memory",
				TargetType: "key_memory",
				TargetID:   key,
				Payload:    map[string]any{"key": key},
				CreatedAt:  time.Now().UTC(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	}
}

func adminOverviewHandler(service *admin.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			userID = "admin"
		}
		overview, err := service.Overview(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "overview_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, overview)
	}
}

func adminUsersHandler(service *admin.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := service.ListUsers(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "users_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func adminUserDetailHandler(service *admin.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		item, err := service.UserDetail(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "user_not_found", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func adminUserSettingsHandler(settings *settingsservice.Service, publisher ports.TaskPublisher, audit *auditsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		userID := chi.URLParam(r, "id")
		before, err := settings.SnapshotForUser(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "settings_failed", err.Error())
			return
		}
		fields := map[string]any{}
		for _, key := range []string{"chat_model", "summary_model", "analysis_model", "embedding_model", "retain_source_materials", "tool_write_enabled", "max_injection_tokens"} {
			if value, ok := payload[key]; ok {
				fields[key] = value
			}
		}
		snapshot, err := settings.UpdateForUser(r.Context(), userID, fields)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "settings_failed", err.Error())
			return
		}
		rebuildRequired := settingsRebuildRequired(before, snapshot, fields)
		recordAdminAudit(r, audit, "update_user_setting", "user_setting", userID, redactSensitiveSettingsFields(fields))
		writeJSON(w, http.StatusOK, map[string]any{
			"settings":                snapshot,
			"memory_rebuild_required": rebuildRequired,
		})
	}
}

func adminUserAPIKeyCreateHandler(service *apikeyservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	type requestBody struct {
		Name string `json:"name"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		userID := chi.URLParam(r, "id")
		record, rawKey, err := service.Create(r.Context(), userID, defaultString(body.Name, "manager-created"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "api_key_failed", err.Error())
			return
		}
		recordAdminAudit(r, audit, "create_api_key", "api_key", record.ID, map[string]any{
			"user_id": userID,
			"name":    record.Name,
			"prefix":  record.Prefix,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"record":  record,
			"api_key": rawKey,
		})
	}
}

func adminUserAPIKeyRevokeHandler(service *apikeyservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keyID := chi.URLParam(r, "keyID")
		if err := service.Revoke(r.Context(), keyID); err != nil {
			writeError(w, http.StatusInternalServerError, "api_key_failed", err.Error())
			return
		}
		recordAdminAudit(r, audit, "revoke_api_key", "api_key", keyID, map[string]any{
			"user_id": chi.URLParam(r, "id"),
		})
		writeJSON(w, http.StatusOK, map[string]any{"revoked": true, "id": keyID})
	}
}

func adminConversationsHandler(service *admin.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := service.ListConversations(r.Context(), userID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "conversations_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func adminConversationDetailHandler(service *admin.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		generationID := strings.TrimSpace(r.URL.Query().Get("generation_id"))
		item, err := service.ConversationDetail(r.Context(), userID, chi.URLParam(r, "id"), generationID)
		if err != nil {
			writeError(w, http.StatusNotFound, "conversation_not_found", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func adminConversationMemoryHandler(service *admin.Service, memories *memoryservice.Service, generations *memorygenservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		conversationID := chi.URLParam(r, "id")
		generationID := strings.TrimSpace(r.URL.Query().Get("generation_id"))
		if memories != nil {
			resolvedGenerationID := generationID
			if resolvedGenerationID == "" {
				if generations != nil {
					generation, err := generations.GetActiveGeneration(r.Context(), userID)
					if err != nil {
						writeError(w, http.StatusInternalServerError, "conversation_memory_failed", err.Error())
						return
					}
					resolvedGenerationID = generation.GenerationID
				} else {
					resolvedGenerationID = memorygenservice.CurrentGenerationID(userID)
				}
			}
			documents, err := memories.ListDocumentsByConversationInGeneration(r.Context(), userID, resolvedGenerationID, conversationID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "conversation_memory_failed", err.Error())
				return
			}
			keyMemories, err := memories.ListKeyMemories(r.Context(), userID, 200)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "conversation_memory_failed", err.Error())
				return
			}
			filtered := make([]domain.KeyMemory, 0)
			for _, item := range keyMemories {
				if item.Value == conversationID || anyString(item.Metadata["conversation_id"]) == conversationID {
					filtered = append(filtered, item)
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"conversation_id": conversationID,
				"generation_id":   resolvedGenerationID,
				"documents":       documents,
				"key_memories":    filtered,
			})
			return
		}
		item, err := service.ConversationMemory(r.Context(), userID, conversationID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "conversation_memory_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func adminUserSourcesHandler(service *memorygenservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "memory_generation_unavailable", "memory generation service is not configured")
			return
		}
		userID := chi.URLParam(r, "id")
		stats, err := service.SourceStatsForUser(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "user_sources_failed", err.Error())
			return
		}
		generations, err := service.ListGenerations(r.Context(), userID, 20)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "user_sources_failed", err.Error())
			return
		}
		active, err := service.GetActiveGeneration(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "user_sources_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user_id":           userID,
			"source_stats":      stats,
			"active_generation": active,
			"generations":       generations,
		})
	}
}

func adminConversationSourcesHandler(service *memorygenservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "memory_generation_unavailable", "memory generation service is not configured")
			return
		}
		userID := queryUserID(r)
		conversationID := chi.URLParam(r, "id")
		latest, ok, err := service.GetLatestSourceSnapshot(r.Context(), userID, conversationID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "conversation_sources_failed", err.Error())
			return
		}
		snapshots, err := service.ListSourceSnapshots(r.Context(), userID, conversationID, 20)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "conversation_sources_failed", err.Error())
			return
		}
		payload := map[string]any{
			"user_id":         userID,
			"conversation_id": conversationID,
			"snapshots":       snapshots,
			"has_snapshot":    ok,
		}
		if ok {
			payload["latest_snapshot"] = latest
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func adminMemoryRebuildListHandler(service *memorygenservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "memory_generation_unavailable", "memory generation service is not configured")
			return
		}
		userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
		if userID == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "user_id is required")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := service.ListGenerations(r.Context(), userID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "memory_rebuilds_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func adminMemoryRebuildGetHandler(service *memorygenservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "memory_generation_unavailable", "memory generation service is not configured")
			return
		}
		userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
		if userID == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "user_id is required")
			return
		}
		item, err := service.GetGeneration(r.Context(), userID, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "memory_rebuild_not_found", err.Error())
			return
		}
		sources, err := service.ListGenerationSources(r.Context(), userID, item.GenerationID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "memory_rebuild_not_found", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"item":    item,
			"sources": sources,
		})
	}
}

func adminMemoryRebuildCreateHandler(service *memorygenservice.Service, publisher ports.TaskPublisher, audit *auditsvc.Service) http.HandlerFunc {
	type requestBody struct {
		Scope                  string `json:"scope"`
		UserID                 string `json:"user_id"`
		ConversationID         string `json:"conversation_id"`
		ActivateOnSuccess      *bool  `json:"activate_on_success"`
		EmbeddingModelOverride string `json:"embedding_model_override"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "memory_generation_unavailable", "memory generation service is not configured")
			return
		}
		if publisher == nil {
			writeError(w, http.StatusServiceUnavailable, "queue_unavailable", "task publisher is not configured")
			return
		}
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		userID := strings.TrimSpace(body.UserID)
		if userID == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "user_id is required")
			return
		}
		activateOnSuccess := true
		if body.ActivateOnSuccess != nil {
			activateOnSuccess = *body.ActivateOnSuccess
		}
		generation, sources, err := service.CreateRebuildGeneration(r.Context(), memorygenservice.CreateRebuildInput{
			UserID:                 userID,
			Scope:                  strings.TrimSpace(body.Scope),
			ConversationID:         strings.TrimSpace(body.ConversationID),
			ActivateOnSuccess:      activateOnSuccess,
			EmbeddingModelOverride: strings.TrimSpace(body.EmbeddingModelOverride),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "memory_rebuild_create_failed", err.Error())
			return
		}
		for _, source := range sources {
			if err := publisher.Publish(r.Context(), domain.TaskMessage{
				Type:      worker.TypeMemoryRebuildConversation,
				Queue:     worker.QueueBulk,
				UniqueKey: worker.TypeMemoryRebuildConversation + ":" + generation.GenerationID + ":" + source.SnapshotID,
				Payload: map[string]any{
					"user_id":       userID,
					"generation_id": generation.GenerationID,
					"snapshot_id":   source.SnapshotID,
				},
			}); err != nil {
				_, _ = service.MarkGenerationSourceFailed(r.Context(), userID, generation.GenerationID, source.SnapshotID, err.Error())
				writeError(w, http.StatusInternalServerError, "memory_rebuild_create_failed", err.Error())
				return
			}
		}
		recordAdminAudit(r, audit, "create_memory_rebuild", "memory_generation", generation.GenerationID, map[string]any{
			"user_id":             userID,
			"scope":               generation.Scope,
			"conversation_id":     generation.ConversationID,
			"source_count":        generation.SourceCount,
			"activate_on_success": generation.ActivateOnSuccess,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"item":    generation,
			"sources": sources,
		})
	}
}

func adminMemoryRebuildActivateHandler(service *memorygenservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	type requestBody struct {
		UserID string `json:"user_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "memory_generation_unavailable", "memory generation service is not configured")
			return
		}
		var body requestBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		userID := strings.TrimSpace(body.UserID)
		if userID == "" {
			userID = strings.TrimSpace(r.URL.Query().Get("user_id"))
		}
		if userID == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "user_id is required")
			return
		}
		item, err := service.ActivateGeneration(r.Context(), userID, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "memory_rebuild_activate_failed", err.Error())
			return
		}
		recordAdminAudit(r, audit, "activate_memory_generation", "memory_generation", item.GenerationID, map[string]any{
			"user_id": userID,
		})
		writeJSON(w, http.StatusOK, item)
	}
}

func adminMemoryDocumentsHandler(service *memoryservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		generationID := strings.TrimSpace(r.URL.Query().Get("generation_id"))
		kinds, err := parseMemoryKinds(r.URL.Query().Get("kind"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		items, err := service.ListDocumentsInGeneration(r.Context(), userID, generationID, kinds, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "memory_documents_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"query": map[string]any{
				"user_id":       userID,
				"generation_id": generationID,
				"kind":          r.URL.Query().Get("kind"),
				"limit":         limit,
			},
		})
	}
}

func adminMemoryDocumentGetHandler(service *memoryservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		generationID := strings.TrimSpace(r.URL.Query().Get("generation_id"))
		kind, err := parseMemoryKind(chi.URLParam(r, "kind"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		item, err := service.GetDocumentInGeneration(r.Context(), userID, generationID, chi.URLParam(r, "id"), kind)
		if err != nil {
			writeError(w, http.StatusNotFound, "memory_document_not_found", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func adminMemoryDocumentUpdateHandler(service *memoryservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	type requestBody struct {
		Content string `json:"content"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		kind, err := parseMemoryKind(chi.URLParam(r, "kind"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		item, err := service.UpdateDocument(r.Context(), userID, chi.URLParam(r, "id"), kind, body.Content)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "memory_document_failed", err.Error())
			return
		}
		if audit != nil {
			audit.Record(r.Context(), domain.AuditEvent{
				ActorType:  "admin",
				ActorID:    "admin",
				Action:     "update_memory_document",
				TargetType: string(kind),
				TargetID:   item.ID,
				Payload: map[string]any{
					"user_id": userID,
				},
				CreatedAt: time.Now().UTC(),
			})
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func adminMemorySearchHandler(service *memoryservice.Service) http.HandlerFunc {
	type requestBody struct {
		Query        string   `json:"query"`
		TopN         int      `json:"top_n"`
		GenerationID string   `json:"generation_id"`
		MemoryTypes  []string `json:"memory_types"`
		TimeTiers    []string `json:"time_tiers"`
		DateFrom     string   `json:"date_from"`
		DateTo       string   `json:"date_to"`
		MinScore     float64  `json:"min_score"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		memoryTypes, err := parseMemoryTypeList(body.MemoryTypes)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		dateFrom, err := parseOptionalRFC3339(body.DateFrom)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		dateTo, err := parseOptionalRFC3339(body.DateTo)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		timeTiers, err := parseTimeTierList(body.TimeTiers)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result, err := service.Search(r.Context(), domain.SearchInput{
			UserID:       userID,
			GenerationID: strings.TrimSpace(body.GenerationID),
			Query:        body.Query,
			TopN:         body.TopN,
			MemoryTypes:  memoryTypes,
			TimeTiers:    timeTiers,
			DateFrom:     dateFrom,
			DateTo:       dateTo,
			MinScore:     body.MinScore,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "memory_search_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func adminMemoryKeysHandler(service *memoryservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		dateFrom, err := parseOptionalRFC3339(r.URL.Query().Get("date_from"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		dateTo, err := parseOptionalRFC3339(r.URL.Query().Get("date_to"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		timeTiers, err := parseTimeTierList(r.URL.Query()["time_tier"])
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		items, err := service.SearchKeyMemories(r.Context(), domain.KeyMemoryQuery{
			UserID:    userID,
			Query:     r.URL.Query().Get("query"),
			TopK:      limit,
			DateFrom:  dateFrom,
			DateTo:    dateTo,
			TimeTiers: timeTiers,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "memory_keys_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"query": map[string]any{
				"user_id": userID,
				"limit":   limit,
			},
		})
	}
}

func adminMemoryKeyGetHandler(service *memoryservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		item, err := findKeyMemoryByID(r.Context(), service, userID, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "key_memory_not_found", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func adminMemoryKeyCreateHandler(service *memoryservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	type requestBody struct {
		Key          string  `json:"key"`
		Value        string  `json:"value"`
		Category     *string `json:"category"`
		ReviewStatus *string `json:"review_status"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if strings.TrimSpace(body.Key) == "" || strings.TrimSpace(body.Value) == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "key and value are required")
			return
		}
		item, err := service.UpsertKeyMemory(r.Context(), domain.KeyMemory{
			UserID:       userID,
			Key:          strings.TrimSpace(body.Key),
			Value:        body.Value,
			Category:     nullableStringValue(body.Category),
			ReviewStatus: defaultString(nullableStringValue(body.ReviewStatus), "pending"),
			Source:       "admin_rest",
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "memory_key_failed", err.Error())
			return
		}
		if audit != nil {
			audit.Record(r.Context(), domain.AuditEvent{
				ActorType:  "admin",
				ActorID:    "admin",
				Action:     "create_key_memory",
				TargetType: "key_memory",
				TargetID:   item.ID,
				Payload: map[string]any{
					"user_id": userID,
					"key":     item.Key,
				},
				CreatedAt: time.Now().UTC(),
			})
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func adminMemoryKeyUpdateHandler(service *memoryservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	type requestBody struct {
		Value        *string `json:"value"`
		Category     *string `json:"category"`
		ReviewStatus *string `json:"review_status"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		item, err := findKeyMemoryByID(r.Context(), service, userID, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "key_memory_not_found", err.Error())
			return
		}
		if body.Value != nil {
			item.Value = *body.Value
		}
		if body.Category != nil {
			item.Category = *body.Category
		}
		if body.ReviewStatus != nil {
			item.ReviewStatus = *body.ReviewStatus
		}
		item.Source = "admin_rest"
		updated, err := service.UpsertKeyMemory(r.Context(), item)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "memory_key_failed", err.Error())
			return
		}
		if audit != nil {
			audit.Record(r.Context(), domain.AuditEvent{
				ActorType:  "admin",
				ActorID:    "admin",
				Action:     "update_key_memory",
				TargetType: "key_memory",
				TargetID:   updated.ID,
				Payload: map[string]any{
					"user_id": userID,
					"key":     updated.Key,
				},
				CreatedAt: time.Now().UTC(),
			})
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func adminMemoryKeyDeleteHandler(service *memoryservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		item, err := findKeyMemoryByID(r.Context(), service, userID, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "key_memory_not_found", err.Error())
			return
		}
		if err := service.DeleteKeyMemory(r.Context(), userID, item.Key); err != nil {
			writeError(w, http.StatusInternalServerError, "memory_key_failed", err.Error())
			return
		}
		if audit != nil {
			audit.Record(r.Context(), domain.AuditEvent{
				ActorType:  "admin",
				ActorID:    "admin",
				Action:     "delete_key_memory",
				TargetType: "key_memory",
				TargetID:   item.ID,
				Payload: map[string]any{
					"user_id": userID,
					"key":     item.Key,
				},
				CreatedAt: time.Now().UTC(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": item.ID})
	}
}

func adminMemoryPreviewHandler(service *admin.Service) http.HandlerFunc {
	type requestBody struct {
		UserID         string                  `json:"user_id"`
		ConversationID string                  `json:"conversation_id"`
		Messages       []domain.UnifiedMessage `json:"messages"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		userID := body.UserID
		if userID == "" {
			userID = queryUserID(r)
		}
		preview, err := service.PreviewMemory(r.Context(), userID, body.ConversationID, body.Messages)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "memory_preview_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, preview)
	}
}

func adminTokenStatsHandler(service *usageservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		totals, err := service.Totals(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "token_stats_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": []map[string]any{{
				"user_id":       userID,
				"input_tokens":  totals["input_tokens"],
				"output_tokens": totals["output_tokens"],
				"total_tokens":  totals["total_tokens"],
			}},
			"query": map[string]any{"user_id": userID},
		})
	}
}

func adminConfigGetHandler(cfg bootstrap.Config, settings *settingsservice.Service, connectionConfig *connectionconfigservice.Service, systemConfig *systemconfigservice.Service, providerConfig *providerconfigservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := queryUserID(r)
		snapshot := map[string]any{}
		if settings != nil {
			var err error
			snapshot, err = settings.SnapshotForUser(r.Context(), userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "config_failed", err.Error())
				return
			}
		}
		if providerConfig != nil {
			providersByScope, err := providerConfig.ListByScope(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "config_failed", err.Error())
				return
			}
			snapshot["provider_sets"] = providersByScope
			snapshot["providers"] = providersByScope[providerconfigservice.ScopeGateway]
		}
		if connectionConfig != nil {
			snapshot["database"] = connectionConfig.Snapshot().Database
		}
		for key, value := range runtimeEnvelope(cfg, systemConfig) {
			snapshot[key] = value
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

func adminConfigPutHandler(cfg bootstrap.Config, settings *settingsservice.Service, connectionConfig *connectionconfigservice.Service, systemConfig *systemconfigservice.Service, providerConfig *providerconfigservice.Service, publisher ports.TaskPublisher, audit *auditsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			userID = queryUserID(r)
		}
		var before map[string]any
		settingsPayload := map[string]any{}
		for _, key := range []string{"chat_model", "summary_model", "analysis_model", "embedding_model", "retain_source_materials", "tool_write_enabled", "max_injection_tokens"} {
			if value, ok := payload[key]; ok {
				settingsPayload[key] = value
			}
		}

		snapshot := map[string]any{}
		if settings != nil {
			var err error
			before, err = settings.SnapshotForUser(r.Context(), userID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "config_failed", err.Error())
				return
			}
			if len(settingsPayload) > 0 {
				snapshot, err = settings.UpdateForUser(r.Context(), userID, settingsPayload)
			} else {
				snapshot, err = settings.SnapshotForUser(r.Context(), userID)
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "config_failed", err.Error())
				return
			}
		}
		if providerConfig != nil {
			if rawProviderSets, ok := payload["provider_sets"]; ok {
				updates, err := providerconfigservice.ParseScopedUpdatePayload(rawProviderSets)
				if err != nil {
					writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
					return
				}
				for scope, scopeUpdates := range updates {
					for providerName, update := range scopeUpdates {
						if _, err := providerConfig.Update(r.Context(), scope, providerName, update); err != nil {
							writeError(w, http.StatusInternalServerError, "config_failed", err.Error())
							return
						}
					}
				}
			}
			if rawProviders, ok := payload["providers"]; ok {
				updates, err := providerconfigservice.ParseUpdatePayload(rawProviders)
				if err != nil {
					writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
					return
				}
				for providerName, update := range updates {
					if _, err := providerConfig.Update(r.Context(), providerconfigservice.ScopeGateway, providerName, update); err != nil {
						writeError(w, http.StatusInternalServerError, "config_failed", err.Error())
						return
					}
				}
			}
			providersByScope, err := providerConfig.ListByScope(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "config_failed", err.Error())
				return
			}
			snapshot["provider_sets"] = providersByScope
			snapshot["providers"] = providersByScope[providerconfigservice.ScopeGateway]
		}
		if rawDatabase, ok := payload["database"]; ok && connectionConfig != nil {
			update, err := parseDatabaseUpdatePayload(rawDatabase)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
			connectionSnapshot, err := connectionConfig.Update(r.Context(), update)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "config_failed", err.Error())
				return
			}
			snapshot["database"] = connectionSnapshot.Database
		} else if connectionConfig != nil {
			snapshot["database"] = connectionConfig.Snapshot().Database
		}
		if rawRuntime, ok := payload["runtime"]; ok && systemConfig != nil {
			runtimePayload, ok := rawRuntime.(map[string]any)
			if !ok {
				writeError(w, http.StatusBadRequest, "invalid_request", "runtime must be an object")
				return
			}
			runtimeSnapshot, err := systemConfig.Update(r.Context(), runtimePayload)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "config_failed", err.Error())
				return
			}
			for key, value := range runtimeSnapshot {
				snapshot[key] = value
			}
		} else {
			for key, value := range runtimeEnvelope(cfg, systemConfig) {
				snapshot[key] = value
			}
		}
		if audit != nil {
			audit.Record(r.Context(), domain.AuditEvent{
				ActorType:  "admin",
				ActorID:    "admin",
				Action:     "update_admin_config",
				TargetType: "system_config",
				TargetID:   userID,
				Payload:    redactConfigPayload(payload),
				CreatedAt:  time.Now().UTC(),
			})
		}
		if len(settingsPayload) > 0 {
			snapshot["memory_rebuild_required"] = settingsRebuildRequired(before, snapshot, settingsPayload)
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

func adminProviderDiagnosticsHandler(cfg bootstrap.Config, systemConfig *systemconfigservice.Service, providerConfig *providerconfigservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if providerConfig == nil {
			writeJSON(w, http.StatusOK, map[string]any{"items": []providerconfigservice.Diagnostic{}})
			return
		}
		desiredGatewayDefault := cfg.DefaultProvider
		liveGatewayDefault := cfg.DefaultProvider
		desiredInternalDefault := cfg.InternalDefaultProvider
		liveInternalDefault := cfg.InternalDefaultProvider
		desiredTimeoutSeconds := int(cfg.ProviderTimeout.Seconds())
		liveTimeoutSeconds := int(cfg.ProviderTimeout.Seconds())
		if systemConfig != nil {
			desired := systemConfig.Snapshot()
			live := systemConfig.LiveSnapshot()
			desiredGatewayDefault = desired.DefaultProvider
			liveGatewayDefault = live.DefaultProvider
			desiredInternalDefault = desired.InternalDefaultProvider
			liveInternalDefault = live.InternalDefaultProvider
			desiredTimeoutSeconds = desired.ProviderTimeoutSeconds
			liveTimeoutSeconds = live.ProviderTimeoutSeconds
		}
		desiredDefaults := map[providerconfigservice.Scope]domain.Provider{
			providerconfigservice.ScopeGateway:   desiredGatewayDefault,
			providerconfigservice.ScopeInternal:  desiredInternalDefault,
			providerconfigservice.ScopeEmbedding: domain.ProviderOpenAICompatible,
		}
		liveDefaults := map[providerconfigservice.Scope]domain.Provider{
			providerconfigservice.ScopeGateway:   liveGatewayDefault,
			providerconfigservice.ScopeInternal:  liveInternalDefault,
			providerconfigservice.ScopeEmbedding: domain.ProviderOpenAICompatible,
		}
		items, err := providerConfig.Diagnostics(r.Context(), desiredDefaults, liveDefaults, desiredTimeoutSeconds, liveTimeoutSeconds)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "provider_diagnostics_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"runtime": map[string]any{
				"desired_gateway_default_provider":  desiredGatewayDefault,
				"live_gateway_default_provider":     liveGatewayDefault,
				"desired_internal_default_provider": desiredInternalDefault,
				"live_internal_default_provider":    liveInternalDefault,
				"desired_timeout_seconds":           desiredTimeoutSeconds,
				"live_timeout_seconds":              liveTimeoutSeconds,
			},
		})
	}
}

func runtimeEnvelope(cfg bootstrap.Config, systemConfig *systemconfigservice.Service) map[string]any {
	if systemConfig != nil {
		return systemConfig.Envelope()
	}
	defaults := systemconfigservice.DefaultsFromConfig(cfg)
	return map[string]any{
		"runtime_defaults": defaults.AsMap(),
		"runtime":          defaults.AsMap(),
		"runtime_live":     defaults.AsMap(),
		"runtime_meta":     map[string]any{},
	}
}

func parseDatabaseUpdatePayload(payload any) (connectionconfigservice.UpdateInput, error) {
	value, ok := payload.(map[string]any)
	if !ok {
		return connectionconfigservice.UpdateInput{}, errors.New("database must be an object")
	}
	update := connectionconfigservice.UpdateInput{}
	if clearValue, exists := value["clear"]; exists {
		clearFlag, ok := clearValue.(bool)
		if !ok {
			return connectionconfigservice.UpdateInput{}, errors.New("database.clear must be a boolean")
		}
		update.Clear = clearFlag
	}
	if rawURL, exists := value["url"]; exists {
		urlValue := strings.TrimSpace(fmt.Sprintf("%v", rawURL))
		update.URL = &urlValue
	}
	return update, nil
}

func adminQueuesHandler(inspector *asynq.Inspector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queueNames := defaultQueueNames()
		items := make([]map[string]any, 0, len(queueNames))
		for _, queueName := range queueNames {
			item := map[string]any{"name": queueName, "size": 0, "pending": 0, "active": 0, "scheduled": 0, "retry": 0}
			if inspector != nil {
				info, err := inspector.GetQueueInfo(queueName)
				if err == nil {
					item["size"] = info.Size
					item["pending"] = info.Pending
					item["active"] = info.Active
					item["scheduled"] = info.Scheduled
					item["retry"] = info.Retry
					item["archived"] = info.Archived
					item["paused"] = info.Paused
					item["latency_ms"] = info.Latency.Milliseconds()
				}
			}
			items = append(items, item)
		}
		writeJSON(w, http.StatusOK, map[string]any{"queues": items})
	}
}

func adminTasksHandler(inspector *asynq.Inspector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if inspector == nil {
			writeJSON(w, http.StatusOK, map[string]any{"items": []map[string]any{}, "query": map[string]any{"limit": 0, "states": []string{}, "queue": ""}})
			return
		}

		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 10
		}
		states := parseTaskStates(r.URL.Query().Get("states"))
		queueName := strings.TrimSpace(r.URL.Query().Get("queue"))
		items := listQueueTasks(inspector, limit, states, queueName)
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
			"query": map[string]any{
				"limit":  limit,
				"states": states,
				"queue":  queueName,
			},
		})
	}
}

func adminTaskDetailHandler(inspector *asynq.Inspector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if inspector == nil {
			writeError(w, http.StatusServiceUnavailable, "queue_unavailable", "task runtime is not configured")
			return
		}
		queueName := strings.TrimSpace(r.URL.Query().Get("queue"))
		taskID := strings.TrimSpace(r.URL.Query().Get("id"))
		if queueName == "" || taskID == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "queue and id are required")
			return
		}

		item, ok := findQueueTask(inspector, queueName, taskID)
		if !ok {
			writeError(w, http.StatusNotFound, "task_not_found", "task was not found in the selected queue")
			return
		}

		queueInfo := map[string]any{}
		if info, err := inspector.GetQueueInfo(queueName); err == nil {
			queueInfo = map[string]any{
				"name":       info.Queue,
				"size":       info.Size,
				"pending":    info.Pending,
				"active":     info.Active,
				"scheduled":  info.Scheduled,
				"retry":      info.Retry,
				"archived":   info.Archived,
				"paused":     info.Paused,
				"latency_ms": info.Latency.Milliseconds(),
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"item":       item,
			"queue_info": queueInfo,
		})
	}
}

func adminCacheHandler(cfg bootstrap.Config, systemConfig *systemconfigservice.Service, health *observability.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		healthStatus := "unknown"
		components := map[string]any{}
		if health != nil {
			result, err := health.Run(r.Context())
			if err == nil {
				healthStatus = "ok"
			} else {
				healthStatus = "degraded"
			}
			for _, component := range result {
				components[component.Name] = map[string]any{
					"status": component.Status,
					"error":  component.Error,
				}
			}
		}

		ttlSeconds := int(cfg.CacheTTL.Seconds())
		if systemConfig != nil {
			ttlSeconds = systemConfig.LiveSnapshot().CacheTTLSeconds
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"l1": map[string]any{
				"enabled":     true,
				"scope":       "process",
				"ttl_seconds": ttlSeconds,
			},
			"l2": map[string]any{
				"enabled":     cfg.RedisURL != "",
				"backend":     ternaryString(cfg.RedisURL != "", "redis", "disabled"),
				"ttl_seconds": ttlSeconds,
			},
			"pubsub": map[string]any{
				"enabled":  cfg.RedisURL != "",
				"channel":  "memstation:cache:invalidate",
				"delivery": "best_effort",
			},
			"notes": []string{
				"TTL is a performance optimization and not a consistency guarantee.",
				"Write paths invalidate L2 first and then fan out best-effort L1 invalidations.",
			},
			"health": map[string]any{
				"status":     healthStatus,
				"components": components,
			},
		})
	}
}

func adminRequeueHandler(publisher ports.TaskPublisher, inspector *asynq.Inspector, audit *auditsvc.Service) http.HandlerFunc {
	type requestBody struct {
		Queue            string `json:"queue"`
		TaskID           string `json:"task_id"`
		TaskType         string `json:"task_type"`
		ConversationID   string `json:"conversation_id"`
		UserID           string `json:"user_id"`
		EmbeddingVersion int    `json:"embedding_version"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if publisher == nil && inspector == nil {
			writeError(w, http.StatusServiceUnavailable, "queue_unavailable", "task runtime is not configured")
			return
		}
		var body requestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if body.Queue != "" && body.TaskID != "" && inspector != nil {
			if err := inspector.RunTask(body.Queue, body.TaskID); err != nil {
				writeError(w, http.StatusBadGateway, "requeue_failed", err.Error())
				return
			}
			recordAdminAudit(r, audit, "requeue_task", "queue_task", body.TaskID, map[string]any{
				"queue": body.Queue,
			})
			writeJSON(w, http.StatusOK, map[string]any{"requeued": true, "queue": body.Queue, "task_id": body.TaskID})
			return
		}
		if publisher == nil {
			writeError(w, http.StatusServiceUnavailable, "queue_unavailable", "task publisher is not configured")
			return
		}
		taskType := body.TaskType
		if taskType == "" {
			taskType = "analysis.extract"
		}
		if !supportsManualTaskPublish(taskType) {
			writeError(w, http.StatusBadRequest, "invalid_request", "manual publish is only supported for conversation.finalize, summary.generate, analysis.extract, and keymemory.reconcile")
			return
		}
		if body.ConversationID == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "conversation_id is required when task_id is not provided")
			return
		}
		if body.UserID == "" {
			body.UserID = r.URL.Query().Get("user_id")
		}
		if body.UserID == "" {
			body.UserID = "admin"
		}
		queue := queueForTaskType(taskType)
		message := domain.TaskMessage{
			Type:  taskType,
			Queue: queue,
			Payload: map[string]any{
				"user_id":         body.UserID,
				"conversation_id": body.ConversationID,
				"summary_ids":     []string{},
			},
		}
		message.UniqueKey = taskType + ":" + body.ConversationID + ":manual"
		err := publisher.Publish(r.Context(), message)
		if err != nil {
			writeError(w, http.StatusBadGateway, "requeue_failed", err.Error())
			return
		}
		recordAdminAudit(r, audit, "requeue_task", "task", taskType, map[string]any{
			"task_type":       taskType,
			"queue":           queue,
			"conversation_id": body.ConversationID,
			"user_id":         body.UserID,
		})
		writeJSON(w, http.StatusOK, map[string]any{"requeued": true, "task_type": taskType, "conversation_id": body.ConversationID, "queue": queue})
	}
}

func parseTaskStates(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{"retry", "archived", "scheduled", "active"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		state := strings.TrimSpace(strings.ToLower(part))
		switch state {
		case "active", "archived", "pending", "retry", "scheduled":
			if _, ok := seen[state]; ok {
				continue
			}
			seen[state] = struct{}{}
			out = append(out, state)
		}
	}
	if len(out) == 0 {
		return []string{"retry", "archived", "scheduled", "active"}
	}
	return out
}

func listQueueTasks(inspector *asynq.Inspector, limit int, states []string, queueName string) []map[string]any {
	if inspector == nil {
		return []map[string]any{}
	}
	queueNames := defaultQueueNames()
	if strings.TrimSpace(queueName) != "" {
		queueNames = []string{strings.TrimSpace(queueName)}
	}
	items := make([]map[string]any, 0, len(queueNames)*len(states)*limit)
	for _, queueName := range queueNames {
		for _, state := range states {
			tasks := loadTasksForState(inspector, queueName, state, limit)
			for _, task := range tasks {
				payload := decodeTaskPayload(task.Payload)
				items = append(items, map[string]any{
					"id":              task.ID,
					"queue":           task.Queue,
					"type":            task.Type,
					"state":           strings.ToLower(task.State.String()),
					"payload":         payload,
					"user_id":         anyString(payload["user_id"]),
					"conversation_id": anyString(payload["conversation_id"]),
					"last_error":      task.LastErr,
					"retried":         task.Retried,
					"max_retry":       task.MaxRetry,
					"last_failed_at":  zeroTimeOrNil(task.LastFailedAt),
					"next_process_at": zeroTimeOrNil(task.NextProcessAt),
					"is_orphaned":     task.IsOrphaned,
				})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left := parseSortableTime(items[i]["last_failed_at"], items[i]["next_process_at"])
		right := parseSortableTime(items[j]["last_failed_at"], items[j]["next_process_at"])
		return left.After(right)
	})
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func defaultQueueNames() []string {
	return []string{"critical", "default", "low", "bulk"}
}

func findQueueTask(inspector *asynq.Inspector, queueName, taskID string) (map[string]any, bool) {
	if inspector == nil || queueName == "" || taskID == "" {
		return nil, false
	}
	for _, state := range []string{"active", "archived", "pending", "scheduled", "retry"} {
		tasks := loadTasksForState(inspector, queueName, state, 500)
		for _, task := range tasks {
			if task.ID != taskID {
				continue
			}
			payload := decodeTaskPayload(task.Payload)
			return map[string]any{
				"id":              task.ID,
				"queue":           task.Queue,
				"type":            task.Type,
				"state":           strings.ToLower(task.State.String()),
				"payload":         payload,
				"user_id":         anyString(payload["user_id"]),
				"conversation_id": anyString(payload["conversation_id"]),
				"last_error":      task.LastErr,
				"retried":         task.Retried,
				"max_retry":       task.MaxRetry,
				"last_failed_at":  zeroTimeOrNil(task.LastFailedAt),
				"next_process_at": zeroTimeOrNil(task.NextProcessAt),
				"is_orphaned":     task.IsOrphaned,
			}, true
		}
	}
	return nil, false
}

func loadTasksForState(inspector *asynq.Inspector, queueName, state string, limit int) []*asynq.TaskInfo {
	opts := []asynq.ListOption{asynq.Page(0), asynq.PageSize(limit)}
	switch state {
	case "active":
		items, _ := inspector.ListActiveTasks(queueName, opts...)
		return items
	case "archived":
		items, _ := inspector.ListArchivedTasks(queueName, opts...)
		return items
	case "pending":
		items, _ := inspector.ListPendingTasks(queueName, opts...)
		return items
	case "scheduled":
		items, _ := inspector.ListScheduledTasks(queueName, opts...)
		return items
	default:
		items, _ := inspector.ListRetryTasks(queueName, opts...)
		return items
	}
}

func decodeTaskPayload(payload []byte) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return map[string]any{"raw": string(payload)}
	}
	return decoded
}

func adminAuditHandler(service *auditsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"items": []domain.AuditEvent{},
				"query": map[string]any{
					"limit": 0,
				},
			})
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		options := auditsvc.ListOptions{
			Limit:      limit,
			ActorType:  strings.TrimSpace(r.URL.Query().Get("actor_type")),
			Action:     strings.TrimSpace(r.URL.Query().Get("action")),
			TargetType: strings.TrimSpace(r.URL.Query().Get("target_type")),
			TargetID:   strings.TrimSpace(r.URL.Query().Get("target_id")),
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": service.ListFiltered(r.Context(), options),
			"query": map[string]any{
				"limit":       limit,
				"actor_type":  options.ActorType,
				"action":      options.Action,
				"target_type": options.TargetType,
				"target_id":   options.TargetID,
			},
		})
	}
}

func queryUserID(r *http.Request) string {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = "admin"
	}
	return userID
}

func setGatewayResponseHeaders(w http.ResponseWriter, req domain.UnifiedRequest) {
	w.Header().Set("X-MemStation-Conversation-ID", req.ConversationID)
	w.Header().Set("X-MemStation-Request-ID", req.RequestID)
	w.Header().Set("X-MemStation-Memory-Persist", strconv.FormatBool(req.PersistMemoryEnabled()))
	w.Header().Set("X-MemStation-Source-Retain", strconv.FormatBool(req.SourceRetainEnabled(false)))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func corsMiddleware(cfg bootstrap.Config, systemConfig *systemconfigservice.Service) func(http.Handler) http.Handler {
	defaultAllowedHeaders := strings.Join([]string{
		"Accept",
		"Authorization",
		"Content-Type",
		"X-Admin-Token",
		"X-MemStation-Admin-Token",
		"X-MemStation-End-Conversation",
		"X-MemStation-Inject-Context",
		"X-MemStation-Inject-Tools",
		"X-MemStation-Memory-Persist",
		"X-MemStation-Mode",
		"X-MemStation-Provider",
		"X-MemStation-Session",
		"X-MemStation-Source-Retain",
		"X-MemStation-Tool-Write",
	}, ", ")
	exposedHeaders := strings.Join([]string{
		"X-MemStation-Conversation-ID",
		"X-MemStation-Request-ID",
		"X-MemStation-Memory-Persist",
		"X-MemStation-Source-Retain",
	}, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			policy := currentCORSOriginPolicy(cfg, systemConfig)
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" && policy.Allows(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders(r, defaultAllowedHeaders))
				w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Expose-Headers", exposedHeaders)
				w.Header().Add("Vary", "Origin")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type corsOriginPolicy struct {
	allowAny bool
	allowed  map[string]struct{}
}

func currentCORSOriginPolicy(cfg bootstrap.Config, systemConfig *systemconfigservice.Service) corsOriginPolicy {
	allowedOrigins := map[string]struct{}{}
	hasConfiguredOrigins := false
	for _, origin := range currentCORSAllowedOrigins(cfg, systemConfig) {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		hasConfiguredOrigins = true
		if origin == "*" {
			continue
		}
		allowedOrigins[origin] = struct{}{}
	}
	return corsOriginPolicy{
		allowAny: !hasConfiguredOrigins,
		allowed:  allowedOrigins,
	}
}

func (p corsOriginPolicy) Allows(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	if p.allowAny {
		return true
	}
	_, ok := p.allowed[origin]
	return ok
}

func corsAllowedHeaders(r *http.Request, fallback string) string {
	requestedHeaders := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers"))
	if requestedHeaders != "" {
		return requestedHeaders
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func readAdminToken(r *http.Request) (string, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") || strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			return "", err
		}
		return r.FormValue("adminToken"), nil
	}
	if strings.HasPrefix(contentType, "application/json") {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return "", err
		}
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		type body struct {
			Token      string `json:"token"`
			AdminToken string `json:"admin_token"`
		}
		var payload body
		if err := json.Unmarshal(raw, &payload); err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		if payload.Token != "" {
			return payload.Token, nil
		}
		return payload.AdminToken, nil
	}
	return "", nil
}

func sessionSameSite(cfg bootstrap.Config, systemConfig *systemconfigservice.Service) http.SameSite {
	if currentSessionCookieCrossSite(cfg, systemConfig) {
		return http.SameSiteNoneMode
	}
	return http.SameSiteStrictMode
}

func currentCORSAllowedOrigins(cfg bootstrap.Config, systemConfig *systemconfigservice.Service) []string {
	if systemConfig == nil {
		return append([]string(nil), cfg.CORSAllowedOrigins...)
	}
	return append([]string(nil), systemConfig.LiveSnapshot().CORSAllowedOrigins...)
}

func currentSessionCookieCrossSite(cfg bootstrap.Config, systemConfig *systemconfigservice.Service) bool {
	if systemConfig == nil {
		return cfg.SessionCookieCrossSite
	}
	return systemConfig.LiveSnapshot().SessionCookieCrossSite
}

func queueForTaskType(taskType string) string {
	switch taskType {
	case "conversation.finalize":
		return "critical"
	case "analysis.extract", "keymemory.reconcile":
		return "low"
	default:
		return "default"
	}
}

func supportsManualTaskPublish(taskType string) bool {
	switch strings.TrimSpace(taskType) {
	case "conversation.finalize", "summary.generate", "analysis.extract", "keymemory.reconcile":
		return true
	default:
		return false
	}
}

func redactConfigPayload(payload map[string]any) map[string]any {
	redacted := map[string]any{}
	for key, value := range payload {
		if strings.EqualFold(key, "embedding_api_key") {
			redacted[key] = "[redacted]"
			continue
		}
		if strings.EqualFold(key, "database") {
			redacted[key] = redactDatabaseSecrets(value)
			continue
		}
		redacted[key] = redactProviderSecrets(value)
	}
	return redacted
}

func redactDatabaseSecrets(value any) any {
	typed, ok := value.(map[string]any)
	if !ok {
		return value
	}
	redacted := map[string]any{}
	for key, nested := range typed {
		if strings.EqualFold(key, "url") {
			redacted[key] = "[redacted]"
			continue
		}
		redacted[key] = redactProviderSecrets(nested)
	}
	return redacted
}

func redactProviderSecrets(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := map[string]any{}
		for key, nested := range typed {
			if strings.EqualFold(key, "api_key") {
				redacted[key] = security.RedactBearerToken(fmt.Sprintf("%v", nested))
				continue
			}
			redacted[key] = redactProviderSecrets(nested)
		}
		return redacted
	case []any:
		redacted := make([]any, 0, len(typed))
		for _, item := range typed {
			redacted = append(redacted, redactProviderSecrets(item))
		}
		return redacted
	default:
		return value
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func nullableStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func anyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprintf("%v", value)
	}
}

func zeroTimeOrNil(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func parseMemoryKinds(value string) ([]domain.MemoryType, error) {
	if strings.TrimSpace(value) == "" {
		return []domain.MemoryType{domain.MemoryTypeSummary, domain.MemoryTypeImpression, domain.MemoryTypeFact}, nil
	}
	parts := strings.Split(value, ",")
	return parseMemoryTypeList(parts)
}

func parseMemoryTypeList(values []string) ([]domain.MemoryType, error) {
	items := make([]domain.MemoryType, 0, len(values))
	for _, raw := range values {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		item, err := parseMemoryKind(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items, nil
}

func parseMemoryKind(value string) (domain.MemoryType, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "summary", "summaries":
		return domain.MemoryTypeSummary, nil
	case "segment", "segments":
		return domain.MemoryTypeSegment, nil
	case "impression", "impressions":
		return domain.MemoryTypeImpression, nil
	case "fact", "facts":
		return domain.MemoryTypeFact, nil
	case "key_memory", "keymemories", "key_memories", "key-memory":
		return domain.MemoryTypeKeyMemory, nil
	default:
		return "", fmt.Errorf("unsupported memory kind: %s", value)
	}
}

func parseTimeTierList(values []string) ([]domain.TimeTier, error) {
	items := make([]domain.TimeTier, 0, len(values))
	for _, raw := range values {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		item := domain.ParseTimeTier(raw)
		if item == "" {
			return nil, fmt.Errorf("unsupported time tier: %s", raw)
		}
		items = append(items, item)
	}
	return items, nil
}

func parseOptionalRFC3339(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("invalid RFC3339 timestamp %q", value)
	}
	return &parsed, nil
}

func findKeyMemoryByID(ctx context.Context, service *memoryservice.Service, userID, id string) (domain.KeyMemory, error) {
	items, err := service.ListKeyMemories(ctx, userID, 500)
	if err != nil {
		return domain.KeyMemory{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.KeyMemory{}, fmt.Errorf("key memory not found: %s", id)
}

func parseSortableTime(values ...any) time.Time {
	for _, value := range values {
		switch typed := value.(type) {
		case time.Time:
			if !typed.IsZero() {
				return typed
			}
		case string:
			parsed, err := time.Parse(time.RFC3339Nano, typed)
			if err == nil && !parsed.IsZero() {
				return parsed
			}
		}
	}
	return time.Time{}
}

func ternaryString(condition bool, whenTrue, whenFalse string) string {
	if condition {
		return whenTrue
	}
	return whenFalse
}

func settingsRebuildRequired(before, after, fields map[string]any) bool {
	if embeddingSettingsChanged(before, after, fields) {
		return true
	}
	for _, key := range []string{"summary_model", "analysis_model"} {
		if _, ok := fields[key]; !ok {
			continue
		}
		if anyString(before[key]) != anyString(after[key]) {
			return true
		}
	}
	return false
}

func embeddingSettingsChanged(before, after, fields map[string]any) bool {
	if len(fields) == 0 {
		return false
	}
	for _, key := range []string{"embedding_model"} {
		if _, ok := fields[key]; !ok {
			continue
		}
		if anyString(before[key]) != anyString(after[key]) {
			return true
		}
	}
	return false
}

func redactSensitiveSettingsFields(fields map[string]any) map[string]any {
	redacted := map[string]any{}
	for key, value := range fields {
		switch key {
		default:
			redacted[key] = value
		}
	}
	return redacted
}

func recordAdminAudit(r *http.Request, audit *auditsvc.Service, action, targetType, targetID string, payload map[string]any) {
	if audit == nil {
		return
	}
	audit.Record(r.Context(), domain.AuditEvent{
		ActorType:  "admin",
		ActorID:    "admin",
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Payload:    payload,
		CreatedAt:  time.Now().UTC(),
	})
}
