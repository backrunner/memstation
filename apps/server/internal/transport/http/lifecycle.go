package httptransport

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	lifecycleservice "memstation/apps/server/internal/service/lifecycle"

	"github.com/go-chi/chi/v5"
)

// chi keeps escaped route segments from URL.RawPath. Memory node identifiers
// contain colons; decode exactly once before the scoped repository lookup.
func memoryRouteID(r *http.Request, name string) string {
	value := chi.URLParam(r, name)
	if r.URL.RawPath == "" {
		return value
	}
	value, err := url.PathUnescape(value)
	if err != nil {
		return ""
	}
	return value
}

func memoryRecoveryBarrier(check func(context.Context) error) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if r.Header.Get("X-MemStation-Internal-Call") != "" && (path == "/v1/chat/completions" || path == "/v1/responses" || path == "/v1/messages") {
				writeError(w, 409, "broker_loop", "AI broker must not route through MemStation gateway")
				return
			}
			if check != nil {
				exempt := strings.HasPrefix(path, "/health/") || path == "/api/admin/config" || path == "/api/admin/login" || path == "/api/admin/logout" || path == "/api/admin/session" || path == "/v1/models" || path == "/api/v1/capabilities" || strings.HasPrefix(path, "/api/admin/workspaces/") && strings.HasSuffix(path, "/lifecycle/capabilities")
				if !exempt {
					if err := check(r.Context()); err != nil {
						writeError(w, 503, "memory_recovery_pending", "memory data is unavailable until deletion history and recovery are verified")
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func lifecycleError(w http.ResponseWriter, err error) {
	if errors.Is(err, lifecycleservice.ErrInvalidInput) {
		writeError(w, 400, "invalid_request", "invalid memory lifecycle input")
		return
	}
	ingestionError(w, err)
}

func registerLifecycleRoutes(r chi.Router, s *lifecycleservice.Service, prefix string) {
	ready := func(w http.ResponseWriter) bool {
		w.Header().Set("Cache-Control", "no-store")
		if s == nil {
			writeError(w, 503, "memory_disabled", "memory lifecycle unavailable")
			return false
		}
		return true
	}
	r.Get(prefix+"/memory/records/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		doc, err := s.Inspect(r.Context(), memoryRouteID(r, "id"))
		if err != nil {
			lifecycleError(w, err)
			return
		}
		writeJSON(w, 200, doc)
	})
	r.Post(prefix+"/memory/candidates", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		var c domain.MemoryCandidate
		if !decodeEventBatch(w, r, &c) {
			return
		}
		out, err := s.Propose(r.Context(), c)
		if err != nil {
			lifecycleError(w, err)
			return
		}
		writeJSON(w, 202, out)
	})
	for _, path := range []string{"/memory/candidates", "/memory/candidates/{id}"} {
		r.Get(prefix+path, func(w http.ResponseWriter, r *http.Request) {
			if !ready(w) {
				return
			}
			out, err := s.Candidates(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("after"))
			if err != nil {
				lifecycleError(w, err)
				return
			}
			if chi.URLParam(r, "id") != "" {
				writeJSON(w, 200, out[0])
				return
			}
			writeJSON(w, 200, map[string]any{"items": out})
		})
	}
	for _, path := range []string{"/sources/retract", "/memory/forget"} {
		r.Post(prefix+path, func(w http.ResponseWriter, r *http.Request) {
			if !ready(w) {
				return
			}
			var q domain.ForgetSelector
			if !decodeEventBatch(w, r, &q) {
				return
			}
			var out domain.MemoryOperation
			var err error
			if strings.HasSuffix(r.URL.Path, "/sources/retract") {
				out, err = s.Retract(r.Context(), q, r.Header.Get("Idempotency-Key"))
			} else {
				out, err = s.Forget(r.Context(), q, r.Header.Get("Idempotency-Key"))
			}
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 202, out)
		})
	}
	r.Get(prefix+"/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		out, err := s.Operation(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			lifecycleError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	r.Post(prefix+"/exports", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		out, err := s.Export(r.Context(), r.Header.Get("Idempotency-Key"))
		if err != nil {
			lifecycleError(w, err)
			return
		}
		writeJSON(w, 202, out)
	})
}

func registerAdminLifecycleRoutes(r chi.Router, deps Dependencies) {
	r.Route("/api/admin/workspaces/{workspaceID}/lifecycle", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if deps.APIKeys == nil || deps.Lifecycle == nil {
					writeError(w, 503, "memory_disabled", "memory lifecycle unavailable")
					return
				}
				ctx, err := deps.APIKeys.Identities().AdministrativeDataContext(r.Context(), r.URL.Query().Get("user_id"), chi.URLParam(r, "workspaceID"), r.URL.Query().Get("partition_id"))
				if err != nil {
					identityError(w, err)
					return
				}
				if security.WritePartition(ctx) != "" && deps.Audit != nil {
					deps.Audit.Record(ctx, domain.AuditEvent{ActorType: "admin", ActorID: "admin", Action: "inspect_private_workspace", TargetType: "workspace", TargetID: chi.URLParam(r, "workspaceID"), Payload: map[string]any{"partition_id": security.WritePartition(ctx)}})
				}
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		})
		registerLifecycleRoutes(r, deps.Lifecycle, "")
		r.Get("/capabilities", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, deps.Lifecycle.Probe(r.Context(), deps.Config.Version))
		})
		r.Get("/generations", adminMemoryRebuildListHandler(deps.Generations))
		r.Post("/generations", adminMemoryRebuildCreateHandler(deps.Generations, deps.Publisher, deps.Audit))
		r.Get("/generations/{id}", adminMemoryRebuildGetHandler(deps.Generations))
		r.Post("/generations/{id}/activate", adminMemoryRebuildActivateHandler(deps.Generations, deps.Audit))
		r.Delete("/generations/{id}", adminMemoryRebuildDiscardHandler(deps.Generations, deps.Audit))
		r.Get("/overview", func(w http.ResponseWriter, r *http.Request) {
			if deps.MemoryControl == nil {
				writeError(w, 503, "unavailable", "memory control unavailable")
				return
			}
			out, err := deps.MemoryControl.Overview(r.Context(), r.URL.Query().Get("after"), 100)
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 200, out)
		})
		r.Post("/jobs/{id}/replay", func(w http.ResponseWriter, r *http.Request) {
			if deps.MemoryControl == nil {
				writeError(w, 503, "unavailable", "memory control unavailable")
				return
			}
			if err := deps.MemoryControl.Replay(r.Context(), chi.URLParam(r, "id")); err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 202, map[string]any{"status": "queued"})
		})
		r.Get("/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
			if deps.Ingestion == nil {
				writeError(w, 503, "unavailable", "ingestion unavailable")
				return
			}
			out, err := deps.Ingestion.Source(r.Context(), chi.URLParam(r, "id"))
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 200, out)
		})
		r.Get("/ingestions/{id}", func(w http.ResponseWriter, r *http.Request) {
			if deps.Ingestion == nil {
				writeError(w, 503, "unavailable", "ingestion unavailable")
				return
			}
			out, err := deps.Ingestion.Progress(r.Context(), chi.URLParam(r, "id"))
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 200, out)
		})
		r.Post("/usage/{id}/reconcile", func(w http.ResponseWriter, r *http.Request) {
			if deps.AIUsage == nil {
				writeError(w, 503, "unavailable", "usage unavailable")
				return
			}
			out, err := deps.AIUsage.Reconcile(r.Context(), chi.URLParam(r, "id"))
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 200, out)
		})
		r.Get("/usage", func(w http.ResponseWriter, r *http.Request) {
			if deps.AIUsage == nil {
				writeError(w, 503, "unavailable", "usage unavailable")
				return
			}
			out, err := deps.AIUsage.List(r.Context(), r.URL.Query().Get("after"), 100)
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"items": out, "usage_authority": "broker", "additional_charge": false})
		})

		r.Post("/memory/candidates/{id}/decision", func(w http.ResponseWriter, r *http.Request) {
			var input domain.CandidateDecision
			if !decodeIdentity(w, r, &input) {
				return
			}
			input.ID = chi.URLParam(r, "id")
			out, err := deps.Lifecycle.Decide(r.Context(), input)
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 200, out)
		})
		r.Get("/publications", func(w http.ResponseWriter, r *http.Request) {
			out, err := deps.Lifecycle.Publications(r.Context(), r.URL.Query().Get("after"))
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"items": out})
		})
		r.Post("/publications", func(w http.ResponseWriter, r *http.Request) {
			var input domain.PublicationInput
			if !decodeEventBatch(w, r, &input) {
				return
			}
			out, err := deps.Lifecycle.Publish(r.Context(), input)
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 201, out)
		})
		r.Post("/publications/{id}/retract", func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				Revision int64 `json:"expected_revision"`
			}
			if !decodeIdentity(w, r, &input) {
				return
			}
			out, err := deps.Lifecycle.RetractPublication(r.Context(), chi.URLParam(r, "id"), input.Revision)
			if err != nil {
				lifecycleError(w, err)
				return
			}
			writeJSON(w, 200, out)
		})
	})
}
