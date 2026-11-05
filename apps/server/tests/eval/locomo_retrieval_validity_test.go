package eval

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorystore "memstation/apps/server/internal/store/sql/memory"
)

type pipelineFixtureEntities struct{ err error }

func (e pipelineFixtureEntities) ExtractQueryEntities(context.Context, string, string) (domain.QueryEntities, error) {
	return domain.QueryEntities{Names: []string{}}, e.err
}

func TestCandidateGateRejectsDegradedHistoricalBaseline(t *testing.T) {
	f := newCandidateGateFixture(t)
	path := f.plan.Baselines[0].Path
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var baseline LOCOMOPipelineReport
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	baseline.Answers[0].EntityRecall = &domain.EntityRecallDiagnostics{Status: "unavailable"}
	f.plan.Baselines[0].SHA256 = writeCandidateFixture(t, path, baseline)
	writeCandidateFixture(t, f.path, f.candidate)
	verdict, err := CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err == nil || !strings.Contains(err.Error(), "entity_recall_incomplete") || verdict.ReplacementEligible {
		t.Fatal("degraded historical answer authorized replacement", verdict, err)
	}
}

func TestEntityRecallQualityValidity(t *testing.T) {
	if pipelineEntityRecallComplete(nil) {
		t.Fatal("missing mandatory recall accepted")
	}
	for _, status := range []string{"complete", "no_entities", "unavailable", "timed_out", "budget_exceeded", "ablation", "", "unknown"} {
		if got := pipelineEntityRecallComplete(&domain.EntityRecallDiagnostics{Status: status}); got != (status == "complete" || status == "no_entities") {
			t.Fatalf("incorrect validity for %q: %t", status, got)
		}
	}
}

func TestPipelineIncompleteEntityRecallDoesNotCallGeneration(t *testing.T) {
	for _, extractionErr := range []error{errors.New("extractor busy"), domain.ErrEntityQueryBudget} {
		t.Run(extractionErr.Error(), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			repo := memorystore.NewInMemoryMemoryRepository()
			if err := repo.StoreDocument(context.Background(), domain.MemoryDocument{ID: "fact", UserID: "owner", Type: domain.MemoryTypeFact, Content: "The bicycle is blue."}); err != nil {
				t.Fatal(err)
			}
			engine := memoryservice.NewEngine(repo, nil)
			engine.SetEntityRecall(pipelineFixtureEntities{err: extractionErr}, time.Second)
			role := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
			runtime := pipelineRuntime{engine: engine, options: LOCOMOPipelineOptions{OutputDir: t.TempDir(), TopN: 5, ContextBytes: 4096, Answerer: role, Judge: role}}
			sample := pipelineSample{ID: "synthetic"}
			question := pipelineQuestion{ID: "synthetic.1", Query: "bicycle", Answer: "blue"}
			answer := runtime.answer(context.Background(), "owner", sample, question, "combined")
			if answer.Error != "entity recall incomplete; degraded retrieval is not a quality result" || answer.Judged || calls.Load() != 0 || answer.AnswerUsage.TotalTokens != 0 || answer.EntityRecall == nil {
				t.Fatalf("incomplete recall reached generation: %+v calls=%d", answer, calls.Load())
			}
			again := runtime.answer(context.Background(), "owner", sample, question, "combined")
			if again.Error != answer.Error || calls.Load() != 0 {
				t.Fatal("resuming rewrote/retried the failed attempt")
			}
		})
	}
}
