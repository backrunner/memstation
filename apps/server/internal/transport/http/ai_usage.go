package httptransport

import (
	"net/http"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/aibroker"
)

func usageQuery(r *http.Request) (domain.AIUsageQuery, error) {
	v := r.URL.Query()
	q := domain.AIUsageQuery{Stage: v.Get("stage"), Strategy: v.Get("strategy"), Status: v.Get("status"), Model: v.Get("model")}
	for key, target := range map[string]*time.Time{"since": &q.Since, "until": &q.Until} {
		if value := v.Get(key); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return q, aibroker.ErrInvalidUsageQuery
			}
			*target = parsed
		}
	}
	return q, nil
}

func aiUsageReportHandler(s *aibroker.UsageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s == nil {
			writeError(w, 503, "unavailable", "usage unavailable")
			return
		}
		q, err := usageQuery(r)
		if err != nil {
			writeError(w, 400, "invalid_request", "invalid usage query")
			return
		}
		report, err := s.Report(r.Context(), q)
		if err == aibroker.ErrInvalidUsageQuery {
			writeError(w, 400, "invalid_request", "invalid usage query")
			return
		}
		if err != nil {
			lifecycleError(w, err)
			return
		}
		writeJSON(w, 200, report)
	}
}
