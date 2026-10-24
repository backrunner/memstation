package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/browse"
	"memstation/apps/server/internal/service/memory"

	"github.com/go-chi/chi/v5"
)

func workspaceSearchError(w http.ResponseWriter, err error) {
	if errors.Is(err, memory.ErrInvalidBrowse) || errors.Is(err, browse.ErrInvalid) {
		writeError(w, 400, "invalid_filters", "Invalid filters or cursor. Return to the first page and retry.")
		return
	}
	if errors.Is(err, domain.ErrAccessNotFound) {
		writeError(w, 404, "not_found", "Memory scope not found.")
		return
	}
	if errors.Is(err, domain.ErrAccessDenied) {
		writeError(w, 403, "forbidden", "Memory scope unavailable.")
		return
	}
	writeError(w, 500, "memory_search_failed", "Unable to load memory results.")
}

func registerWorkspaceSearchRoutes(r chi.Router, service *memory.Service) {
	r.Get("/api/admin/workspaces/{workspaceID}/memory/documents/page", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		kinds, err := parseMemoryTypeList(strings.Split(q.Get("kind"), ","))
		if err != nil {
			workspaceSearchError(w, memory.ErrInvalidBrowse)
			return
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		result, err := service.BrowseDocuments(r.Context(), domain.MemoryBrowseQuery{UserID: queryUserID(r), GenerationID: q.Get("generation_id"), Kinds: kinds, Query: q.Get("q"), After: q.Get("after"), Limit: limit})
		if err != nil {
			workspaceSearchError(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	r.Post("/api/admin/workspaces/{workspaceID}/memory/search", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query       string   `json:"query"`
			TopN        int      `json:"top_n"`
			MemoryTypes []string `json:"memory_types"`
			AsOf        string   `json:"as_of"`
			KnownAt     string   `json:"known_at"`
			DateFrom    string   `json:"date_from"`
			DateTo      string   `json:"date_to"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || len(body.Query) > 1024 || body.TopN < 1 || body.TopN > 100 {
			workspaceSearchError(w, memory.ErrInvalidBrowse)
			return
		}
		kinds, err := parseMemoryTypeList(body.MemoryTypes)
		if err != nil {
			workspaceSearchError(w, memory.ErrInvalidBrowse)
			return
		}
		input := domain.SearchInput{UserID: queryUserID(r), GenerationID: r.URL.Query().Get("generation_id"), Query: strings.TrimSpace(body.Query), TopN: body.TopN, MemoryTypes: kinds, IncludeSegments: len(kinds) == 0}
		for _, value := range []struct {
			text   string
			target **time.Time
		}{{body.AsOf, &input.AsOf}, {body.KnownAt, &input.KnownAt}, {body.DateFrom, &input.DateFrom}, {body.DateTo, &input.DateTo}} {
			*value.target, err = parseOptionalRFC3339(value.text)
			if err != nil {
				workspaceSearchError(w, memory.ErrInvalidBrowse)
				return
			}
		}
		if input.DateFrom != nil && input.DateTo != nil && input.DateFrom.After(*input.DateTo) {
			workspaceSearchError(w, memory.ErrInvalidBrowse)
			return
		}
		if input.AsOf != nil || input.KnownAt != nil {
			for _, kind := range kinds {
				if kind != domain.MemoryTypeFact {
					workspaceSearchError(w, memory.ErrInvalidBrowse)
					return
				}
			}
			input.MemoryTypes = []domain.MemoryType{domain.MemoryTypeFact}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		result, err := service.Search(ctx, input)
		if err != nil {
			workspaceSearchError(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
}
