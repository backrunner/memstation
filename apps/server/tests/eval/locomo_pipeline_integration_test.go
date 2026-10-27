package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/service/memoryllm"
	"memstation/apps/server/internal/testutil/memoryllmfixture"
)

// Dedicated empty migrated database only; this contract does not reset shared data.
func pipelineContractProvider(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" && r.URL.Path != "/chat/completions" && r.URL.Path != "/rerank" {
			// Ignore local port-discovery probes; they are not model calls.
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		if r.URL.Path == "/rerank" {
			var body struct {
				Documents []string `json:"documents"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			results := []any{}
			for i := range body.Documents {
				results = append(results, map[string]any{"index": i, "relevance_score": 0.5})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
			return
		}

		if strings.HasSuffix(r.URL.Path, "embeddings") {
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "data": []any{map[string]any{"embedding": []float32{1, 0, 0}}}})
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 {
			t.Errorf("bad provider request: %s %s messages=%d error=%v", r.Method, r.URL.Path, len(body.Messages), err)
			w.WriteHeader(400)
			return
		}
		system, input := body.Messages[0].Content, body.Messages[1].Content
		var output json.RawMessage
		var err error
		switch {
		case system == pipelineAnswerInstruction:
			if strings.Contains(input, "GOLD_SENTINEL") {
				t.Error("answerer received the reference answer")
			}
			output = json.RawMessage(`{"answer":"unknown"}`)
		case system == pipelineJudgeInstruction:
			output = json.RawMessage(`{"correct":false,"reason":"simulated contract judge"}`)
		case strings.Contains(system, "Score the supplied memory"):
			var in struct {
				Reference string `json:"reference_time"`
			}
			_ = json.Unmarshal([]byte(input), &in)
			output, _ = json.Marshal(map[string]any{"importance": 1, "decay_factor": 1, "activity_at": in.Reference, "key_memory_candidate": false, "reason": "fixture"})
		default:
			if strings.Contains(input, "GOLD_SENTINEL") {
				t.Error("ingestion received gold")
			}
			task := "analysis.extract"
			if strings.Contains(system, "Summarize the supplied") {
				task = "summary.generate"
			}
			output, err = (memoryllmfixture.Generator{}).Generate(r.Context(), memoryllm.Request{Task: task, Input: json.RawMessage(input)})
		}
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "model": "fixture", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": string(output)}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
	}))

	return server, &calls
}

func TestLOCOMOPipelinePostgresReplayResumeAndLease(t *testing.T) {
	dsn := os.Getenv("MEMSTATION_PIPELINE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated pipeline contract database is not configured")
	}
	server, calls := pipelineContractProvider(t)
	defer server.Close()
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
	o := LOCOMOPipelineOptions{DatasetPath: pipelineFixture(t), DatabaseURL: dsn, OutputDir: t.TempDir(), Prefix: "eval-contract", TopN: 10, ContextBytes: 4096, CodeRevision: "contract", EmbeddingRevision: "fixture", Embedding: EmbeddingOptions{Mode: EmbeddingModeProvider, Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}, Extraction: cfg, Answerer: cfg, Judge: cfg, Rerank: PipelineRerankOptions{Model: "rerank_compatible:fixture", ModelRevision: "fixture", BaseURL: server.URL}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	report, err := RunLOCOMOPipeline(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if report.CompletedSessions != 2 || len(report.Answers) != 6 || report.Modes["combined"].Judged != 2 {
		t.Fatalf("incomplete contract: %+v", report.Modes)
	}
	for _, a := range report.Answers {
		if a.Rerank.Status != "applied" {
			t.Fatalf("%s %s did not use production reranker: %+v", a.QuestionID, a.Mode, a.Rerank)
		}
	}
	before := calls.Load()
	if _, err = RunLOCOMOPipeline(ctx, o); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatal("resume repeated model calls")
	}
	raw, _ := json.Marshal(report.Manifest)
	o.manifestHash = pipelineHash(string(raw))
	runtime, err := newPipelineRuntime(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if second, e := newPipelineRuntime(ctx, o); e == nil {
		second.close()
		t.Error("concurrent runner was accepted")
	}
	runtime.close()
	runtime, err = newPipelineRuntime(ctx, o)
	if err != nil {
		t.Fatal("lease not released:", err)
	}
	runtime.close()
	changedRoute := o
	changedRoute.Answerer.BaseURL += "/another-route"
	if _, err = RunLOCOMOPipeline(ctx, changedRoute); err == nil {
		t.Fatal("changed endpoint path accepted")
	}
	o.CodeRevision = "changed"
	if _, err = RunLOCOMOPipeline(ctx, o); err == nil {
		t.Fatal("mixed source revisions accepted")
	}
	// Explicitly confirm reference roles were retained in persisted source.
	db, err := OpenEvaluationDatabase(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE role=$1`, string(domain.RoleUser)).Scan(&count); err != nil || count < 3 {
		t.Fatalf("lost human source roles: count=%d err=%v", count, err)
	}
}

func TestPipelineConcurrentOwnersShareAuthorityBoundDispatcher(t *testing.T) {
	dsn := os.Getenv("MEMSTATION_PIPELINE_PARALLEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated parallel pipeline contract database required")
	}
	server, _ := pipelineContractProvider(t)
	defer server.Close()
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
	o := LOCOMOPipelineOptions{DatabaseURL: dsn, OutputDir: t.TempDir(), Prefix: "eval-parallel", Modes: []string{"derived"}, Embedding: EmbeddingOptions{Mode: EmbeddingModeProvider, Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}, Extraction: cfg, TaskConcurrency: 4}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	runtime, err := newPipelineRuntime(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.close()
	samples, err := loadPipelineSamples(pipelineFixture(t), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for i := range 4 {
		wg.Go(func() {
			owner := fmt.Sprintf("eval-parallel-%d", i)
			sample := samples[0]
			sample.ID = fmt.Sprintf("parallel-%d", i)
			stages, err := runtime.ingest(ctx, owner, sample)
			if err != nil {
				failures <- err
				return
			}
			if len(stages) != 2 {
				failures <- fmt.Errorf("owner %d missing completed sessions", i)
				return
			}
			for _, stage := range stages {
				for _, doc := range stage.Documents {
					if doc.UserID != owner {
						failures <- fmt.Errorf("foreign output reached owner %d", i)
					}
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	runtime.drainMu.Lock()
	defer runtime.drainMu.Unlock()
	if runtime.taskQueue == nil || len(runtime.taskQueue.state.Pending) != 0 {
		t.Fatal("shared journal did not drain")
	}
	for _, attempt := range runtime.taskQueue.state.Attempts {
		if attempt.Error != "" {
			t.Fatalf("concurrent authority failure: %+v", attempt)
		}
	}
}

// The real repositories and authority-bound dispatcher must isolate a terminal
// producer failure, not only successful concurrent producers.
type ownerFailingPipelineAdapter struct {
	ports.ProviderAdapter
	owner string
}

func (a ownerFailingPipelineAdapter) Complete(ctx context.Context, request domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	if request.UserID == a.owner {
		return nil, errors.New("injected owner-specific generation failure")
	}
	return a.ProviderAdapter.Complete(ctx, request)
}

func TestPipelinePostgresFailureIsolation(t *testing.T) {
	dsn := os.Getenv("MEMSTATION_PIPELINE_FAILURE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated migrated pipeline database required")
	}
	server, _ := pipelineContractProvider(t)
	defer server.Close()
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
	options := LOCOMOPipelineOptions{TaskConcurrency: 4, DatasetPath: pipelineFixture(t), DatabaseURL: dsn, OutputDir: t.TempDir(), Prefix: "eval-failure-contract", Modes: []string{"derived"}, TopN: 10, ContextBytes: 4096, CodeRevision: "contract", EmbeddingRevision: "fixture", Embedding: EmbeddingOptions{Mode: EmbeddingModeProvider, Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}, Extraction: cfg, Answerer: cfg, Judge: cfg, extractionAdapter: ownerFailingPipelineAdapter{ProviderAdapter: pipelineAdapter(cfg), owner: "eval-failure-contract-broken"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	runtime, err := newPipelineRuntime(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.close()
	samples, err := loadPipelineSamples(options.DatasetPath, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, owner := range []string{"eval-failure-contract-broken", "eval-failure-contract-healthy"} {
		wg.Go(func() {
			stages, err := runtime.ingest(ctx, owner, samples[0])
			if owner == "eval-failure-contract-broken" {
				if err == nil {
					t.Error("failed producer accepted as complete")
				}
				return
			}
			if err != nil || len(stages) != 2 {
				t.Errorf("unrelated producer failure blocked healthy owner: %d %v", len(stages), err)
			}
		})
	}
	wg.Wait()
}
