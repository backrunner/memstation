package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/ingestion"

	"github.com/go-chi/chi/v5"
)

func ingestionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ingestion.ErrInvalidEvent):
		writeError(w, 400, "invalid_event", "invalid source event")
	case errors.Is(err, ingestion.ErrIdempotencyConflict):
		writeError(w, 409, "idempotency_conflict", "idempotency key has a different payload")
	case errors.Is(err, domain.ErrAccessDenied), errors.Is(err, domain.ErrAccessNotFound), errors.Is(err, domain.ErrAccessConflict):
		identityError(w, err)
	default:
		writeError(w, 503, "memory_unavailable", "memory operation unavailable")
	}
}

func registerIngestionRoutes(r chi.Router, s *ingestion.Service) {
	ready := func(w http.ResponseWriter) bool {
		w.Header().Set("Cache-Control", "no-store")
		if s == nil {
			writeError(w, 503, "memory_disabled", "durable ingestion unavailable")
			return false
		}
		return true
	}
	r.Post("/api/v1/conversations", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		var input domain.ConversationWindowInput
		if !decodeIdentity(w, r, &input) {
			return
		}
		out, err := s.OpenWindow(r.Context(), input, r.Header.Get("Idempotency-Key"))
		if err != nil {
			ingestionError(w, err)
			return
		}
		writeJSON(w, 201, out)
	})
	r.Get("/api/v1/conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		out, err := s.Window(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			ingestionError(w, err)
			return
		}
		writeJSON(w, 200, out)
	})
	r.Post("/api/v1/events/batch", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		var batch domain.ObserveBatch
		if !decodeEventBatch(w, r, &batch) {
			return
		}
		receipt, err := s.Observe(r.Context(), batch, r.Header.Get("Idempotency-Key"))
		if err != nil {
			ingestionError(w, err)
			return
		}
		if err = security.RequireCapability(r.Context(), domain.CapabilityEventsWrite); err != nil {
			ingestionError(w, err)
			return
		}
		writeJSON(w, 202, receipt)
	})
	r.Get("/api/v1/ingestions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		receipt, err := s.Progress(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			ingestionError(w, err)
			return
		}
		writeJSON(w, 200, receipt)
	})
	r.Get("/api/v1/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		source, err := s.Source(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			ingestionError(w, err)
			return
		}
		if err = security.RequireCapability(r.Context(), domain.CapabilitySourcesRead); err != nil {
			ingestionError(w, err)
			return
		}
		writeJSON(w, 200, source)
	})
	r.Post("/api/v1/conversations/{id}/finalize", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		var input struct {
			ExpectedRevision int64 `json:"expected_revision"`
		}
		if !decodeIdentity(w, r, &input) {
			return
		}
		if err := s.Finalize(r.Context(), chi.URLParam(r, "id"), input.ExpectedRevision); err != nil {
			ingestionError(w, err)
			return
		}
		writeJSON(w, 202, map[string]string{"status": "accepted"})
	})
}

func decodeEventBatch(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		ingestionError(w, ingestion.ErrInvalidEvent)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		ingestionError(w, ingestion.ErrInvalidEvent)
		return false
	}
	return true
}
