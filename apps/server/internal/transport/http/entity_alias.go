package httptransport

import (
	"encoding/json"
	"net/http"

	"memstation/apps/server/internal/domain"
	auditsvc "memstation/apps/server/internal/service/audit"
	memoryservice "memstation/apps/server/internal/service/memory"
)

func adminEntityAliasesHandler(s *memoryservice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.ListEntityAliases(r.Context(), queryUserID(r), r.URL.Query().Get("generation_id"))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "entity_alias_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, items)
	}
}

func adminEntityAliasPutHandler(s *memoryservice.Service, audit *auditsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input domain.EntityAlias
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		input.UserID = queryUserID(r)
		item, err := s.PutEntityAlias(r.Context(), input)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_entity_alias", err.Error())
			return
		}
		if audit != nil {
			audit.Record(r.Context(), domain.AuditEvent{ActorType: "admin", ActorID: "admin", Action: "memory.entity_alias.review", TargetType: "entity_alias", TargetID: item.ID, Payload: map[string]any{"user_id": item.UserID, "status": item.Status}})
		}
		writeJSON(w, http.StatusOK, item)
	}
}
