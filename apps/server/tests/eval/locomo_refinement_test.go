package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

func TestPipelineRefinementNoGoldLeakAndImmutableFailures(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "invalid_plan"}[broken], func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
					MaxTokens int `json:"max_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
					t.Error("bad model request")
					w.WriteHeader(400)
					return
				}
				prompt, data := request.Messages[0].Content, request.Messages[1].Content
				if prompt != pipelineJudgeInstruction && (strings.Contains(data, "GOLD_SENTINEL") || strings.Contains(data, "D999:99")) {
					t.Error("gold reached planner/reader")
				}
				output := `{"answer":"bicycle"}`
				switch prompt {
				case memoryservice.EvidencePlanInstruction:
					aspect := "events"
					if broken {
						aspect = ""
					}
					raw, _ := json.Marshal(map[string]any{"aspects": []string{aspect}, "queries": []any{map[string]string{"query": "bicycle city", "reason": "other events"}}})
					output = string(raw)
				case memoryservice.EvidenceSelectionInstruction:
					output = `{"selected_ids":["m1"],"checks":[{"aspect":"events","evidence_ids":["m1"],"gap":""}]}`
				case pipelineCoverageInstruction:
				case pipelineJudgeInstruction:
					output = `{"correct":true,"reason":"fixture"}`
				default:
					t.Error("unexpected instruction")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": output}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
			}))
			defer server.Close()
			repo := memorystore.NewInMemoryMemoryRepository()
			user := locomoUserID("s", "")
			if err := repo.StoreDocument(context.Background(), domain.MemoryDocument{ID: "base", UserID: user, Type: domain.MemoryTypeFact, Content: "bicycle city"}); err != nil {
				t.Fatal(err)
			}
			cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
			runtime := pipelineRuntime{options: LOCOMOPipelineOptions{OutputDir: t.TempDir(), ContextBytes: 4096, TopN: 5, ContextFormat: "plain", ReaderPolicy: "coverage", RefineEvidence: true, Planner: cfg, Answerer: cfg, Judge: cfg, manifestHash: "fixture"}, raw: &harness{engine: memoryservice.NewEngine(repo, nil)}}
			q := pipelineQuestion{ID: "s.1", Query: "bicycle events", Answer: "GOLD_SENTINEL", Evidence: []string{"D999:99"}}
			a := runtime.answer(context.Background(), user, pipelineSample{ID: "s"}, q, "raw")
			if broken {
				if a.Error == "" || a.Judged || calls.Load() != 1 {
					t.Fatal("invalid plan advanced", a.Error, calls.Load())
				}
			} else if a.Error != "" || !a.Judged || len(a.Refinement.Steps) != 1 || calls.Load() != 4 || a.PlannerUsage.InputTokens != 20 {
				t.Fatal("pipeline missing refinement", a.Error, a.Refinement, calls.Load())
			}
			before := calls.Load()
			again := runtime.answer(context.Background(), user, pipelineSample{ID: "s"}, q, "raw")
			if calls.Load() != before || again.Error != a.Error {
				t.Fatal("resume retried immutable attempt")
			}
		})
	}
}

func TestPipelineRefinementRejectsOracleAndUnknownReader(t *testing.T) {
	for _, o := range []LOCOMOPipelineOptions{{ReaderPolicy: "unknown"}, {RefineEvidence: true, Modes: []string{"gold_evidence"}}} {
		if err := normalizePipelineProtocol(&o); err == nil {
			t.Fatal("invalid protocol accepted")
		}
	}
}

func TestRefinementSummaryIncludesPlannerAndFollowupCosts(t *testing.T) {
	answers := []PipelineAnswer{}
	for i := 1; i <= 20; i++ {
		answers = append(answers, PipelineAnswer{Mode: "raw", Judged: true, Correct: true, SearchMillis: float64(i), EvidenceMillis: 1, PlannerUsage: domain.UsageRecord{InputTokens: 100, OutputTokens: 10}, Refinement: memoryservice.RefinementDiagnostics{Status: "refined", Millis: 1000, Steps: []memoryservice.RefinementStep{{Rerank: memoryservice.RerankDiagnostics{Status: "applied"}}}}})
	}
	got := summarizePipelineAnswers(answers)["raw"]
	if got.SearchP95Millis != 19 || got.EvidenceReadyP95Millis != 1020 || got.MeanPlannerInputTokens != 100 || got.MeanPlannerOutputTokens != 10 || got.RefinementStatuses["refined"] != 20 || got.FollowupRerankStatuses["applied"] != 20 {
		t.Fatalf("planner cost hidden: %+v", got)
	}
}
