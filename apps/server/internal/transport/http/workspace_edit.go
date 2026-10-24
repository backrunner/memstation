package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/memory"

	"github.com/go-chi/chi/v5"
)

func memoryEditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, memory.ErrInvalidEdit):
		writeError(w, 400, "invalid_request", "invalid memory edit")
	case errors.Is(err, domain.ErrAccessNotFound):
		writeError(w, 404, "not_found", "memory not found in this workspace")
	case errors.Is(err, domain.ErrAccessConflict):
		writeError(w, 409, "revision_conflict", "memory changed; load the current version before saving")
	case errors.Is(err, domain.ErrAccessDenied):
		writeError(w, 403, "access_denied", "memory edit denied")
	default:
		writeError(w, 500, "memory_edit_failed", "memory edit failed")
	}
}

func decodeMemoryEdit(w http.ResponseWriter, r *http.Request, body any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		memoryEditError(w, memory.ErrInvalidEdit)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		memoryEditError(w, memory.ErrInvalidEdit)
		return false
	}
	return true
}

func registerWorkspaceEditRoutes(r chi.Router, service *memory.Service) {
	const base = "/api/admin/workspaces/{workspaceID}/memory"
	r.Put(base+"/documents/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			domain.MemoryWriteCondition
			Content string `json:"content"`
		}
		if !decodeMemoryEdit(w, r, &body) {
			return
		}
		if explicit := r.URL.Query().Get("generation_id"); explicit != "" && explicit != body.GenerationID {
			memoryEditError(w, memory.ErrInvalidEdit)
			return
		}
		item, err := service.EditDocument(r.Context(), queryUserID(r), chi.URLParam(r, "id"), domain.MemoryType(chi.URLParam(r, "kind")), body.Content, body.MemoryWriteCondition)
		if err != nil {
			memoryEditError(w, err)
			return
		}
		writeJSON(w, 200, item)
	})
	r.Get(base+"/keys", adminMemoryKeysHandler(service))
	r.Get(base+"/keys/{id}", adminMemoryKeyGetHandler(service))
	save := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				domain.MemoryWriteCondition
				Key          string `json:"key"`
				Value        string `json:"value"`
				Category     string `json:"category"`
				ReviewStatus string `json:"review_status"`
			}
			if !decodeMemoryEdit(w, r, &body) {
				return
			}
			body.Create = create
			if !create && body.Key != "" {
				memoryEditError(w, memory.ErrInvalidEdit)
				return
			}
			item, err := service.EditKeyMemory(r.Context(), queryUserID(r), chi.URLParam(r, "id"), body.Key, body.Value, body.Category, body.ReviewStatus, body.MemoryWriteCondition)
			if err != nil {
				memoryEditError(w, err)
				return
			}
			status := 200
			if create {
				status = 201
			}
			writeJSON(w, status, item)
		}
	}
	r.Post(base+"/keys", save(true))
	r.Put(base+"/keys/{id}", save(false))
	r.Delete(base+"/keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body domain.MemoryWriteCondition
		if !decodeMemoryEdit(w, r, &body) {
			return
		}
		if err := service.RemoveKeyMemory(r.Context(), queryUserID(r), chi.URLParam(r, "id"), body); err != nil {
			memoryEditError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
	})
}
