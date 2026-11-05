package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/processing"
)

func TestPreviewRespectsLightweightAndHybridBudgetWithoutLLM(t *testing.T) {
	for _, strategy := range []string{"lightweight", "hybrid"} {
		t.Run(strategy, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing encoder authentication")
				}
				_ = json.NewEncoder(w).Encode(processing.Response{Version: 1, Model: "encoder", Revision: "pinned", Splitter: "char", Observations: []processing.Observation{{Supported: true, NeedsSemantic: 1, Spans: []processing.Span{{Start: 0, End: 2, Text: "上海", Label: "location", Confidence: .9}}}}})
			}))
			defer server.Close()
			policy, err := processing.New(processing.Config{EntityRecallTimeoutMS: 200, Strategy: strategy, Endpoint: server.URL, Token: "fixture", Model: "encoder", Revision: "pinned", Splitter: "char", Threshold: .75, MaxSemanticBytes: 1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			service := &Service{processingPolicy: policy}
			preview, err := service.PreviewMemory(context.Background(), "owner", "preview", []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "上海是我的出发地"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(preview.GenericItems) != 1 || len(preview.Facts) != 0 || len(preview.Summary) != 0 {
				t.Fatalf("unexpected semantic preview: %+v", preview)
			}
			want := "not_requested"
			if strategy == "hybrid" {
				want = "deferred_budget"
			}
			if preview.Metadata["semantic_status"] != want {
				t.Fatalf("status: %+v", preview.Metadata)
			}
		})
	}
}
