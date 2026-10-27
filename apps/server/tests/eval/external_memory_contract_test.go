package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

func TestArenaAdapterRejectsIncompleteAndDuplicateChains(t *testing.T) {
	valid := `{"id":0,"paper_name":"paper","questions":["q1","q2"],"answers":["gold1","gold2"],"backgrounds":["b1","b2"]}`
	tasks, err := loadArenaTasks([]byte(valid))
	if err != nil || len(tasks) != 1 || len(tasks[0].Questions) != 2 {
		t.Fatal(tasks, err)
	}
	for _, raw := range []string{"", valid + "\n" + valid, strings.Replace(valid, `"gold1","gold2"`, `"gold1"`, 1), strings.Replace(valid, `"b1","b2"`, `"b1"`, 1)} {
		if _, err := loadArenaTasks([]byte(raw)); err == nil {
			t.Fatal("accepted missing or duplicate steps")
		}
	}
}

func TestExternalContextBudgetKeepsWholeSourcesAndHidesLabels(t *testing.T) {
	docs := []domain.MemoryDocument{
		{ID: "dataset-gold-id", Content: strings.Repeat("oversized", 100), Metadata: map[string]any{"has_answer": true, "question_type": "gold-type"}},
		{ID: "stored-id", Content: "Alice did not visit 北京.", Metadata: map[string]any{"session_date": "2024/01/01", "answer": "SECRET_REFERENCE"}},
	}
	text, ids, omitted := externalContext(docs, 160)
	if len(text) > 160 || omitted != 1 || len(ids) != 1 || ids[0] != "stored-id" || !strings.Contains(text, "did not visit 北京") || !strings.Contains(text, "2024/01/01") {
		t.Fatal("budget, provenance or negation lost", text, ids, omitted)
	}
	for _, secret := range []string{"dataset-gold-id", "stored-id", "has_answer", "gold-type", "SECRET_REFERENCE"} {
		if strings.Contains(text, secret) {
			t.Fatal("internal identity or annotation entered context", secret)
		}
	}
}

type externalFixtureEmbedding struct{ calls atomic.Int64 }

func (e *externalFixtureEmbedding) Embed(_ context.Context, _ domain.EmbeddingInput) (domain.EmbeddingResult, error) {
	e.calls.Add(1)
	return domain.EmbeddingResult{Vector: []float32{1, 0}, Model: "fixture", Dimensions: 2}, nil
}

type externalBlockingReranker struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (r *externalBlockingReranker) Rerank(_ context.Context, _ domain.RerankInput) ([]domain.RerankResult, error) {
	r.calls.Add(1)
	r.entered <- struct{}{}
	<-r.release
	return nil, nil
}

func TestExternalRerankQueueHonorsCancellationBeforeInference(t *testing.T) {
	d := &externalBlockingReranker{entered: make(chan struct{}, 1), release: make(chan struct{})}
	r := externalSerialReranker{delegate: d}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.Rerank(context.Background(), domain.RerankInput{})
	}()
	<-d.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Rerank(ctx, domain.RerankInput{})
	close(d.release)
	<-done
	if err != context.Canceled || d.calls.Load() != 1 {
		t.Fatal("cancelled queue entry invoked provider", err, d.calls.Load())
	}
}

func TestExternalEmbeddingCachePinsModelRevisionAndContent(t *testing.T) {
	d := &externalFixtureEmbedding{}
	c := &externalEmbeddingCache{delegate: d, dir: t.TempDir(), model: "fixture", revision: "revision-a"}
	in := domain.EmbeddingInput{Text: "same source", UserID: "one"}
	for range 2 {
		if _, err := c.Embed(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	in.UserID = "two" // A computation cache, never a document-authorization cache.
	if _, err := c.Embed(context.Background(), in); err != nil || d.calls.Load() != 1 {
		t.Fatal("identical content did not reuse computation", err)
	}
	c.revision = "revision-b"
	if _, err := c.Embed(context.Background(), in); err != nil || d.calls.Load() != 2 {
		t.Fatal("model revision reused stale embedding", err)
	}
	key := pipelineHash(c.revision + "\x00fixture\x00" + in.Text)
	if err := os.WriteFile(filepath.Join(c.dir, key+".json"), []byte(`{"vector":[0,0],"dimensions":2,"model":"fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Embed(context.Background(), in); err == nil {
		t.Fatal("corrupt cache silently accepted")
	}
}

func TestArenaSequentialMemoryNeverReceivesReferenceOrJudgeFeedback(t *testing.T) {
	var readCalls, gradeCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rerank" {
			var request struct {
				Documents []string `json:"documents"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			rows := make([]map[string]any, len(request.Documents))
			for i, doc := range request.Documents {
				if strings.Contains(doc, "REFERENCE_SECRET") || strings.Contains(doc, "JUDGE_SECRET") {
					t.Error("grading signal indexed")
				}
				rows[i] = map[string]any{"index": i, "relevance_score": 0.9}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
			return
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		content := "no"
		if request.Model == "reader" {
			n := readCalls.Add(1)
			wire, _ := json.Marshal(request)
			if strings.Contains(string(wire), "REFERENCE_SECRET") || strings.Contains(string(wire), "JUDGE_SECRET") {
				t.Error("reference entered solver")
			}
			var payload struct {
				Problem  string `json:"problem"`
				Memories string `json:"memories"`
			}
			if len(request.Messages) != 2 || json.Unmarshal([]byte(request.Messages[1].Content), &payload) != nil {
				t.Error("invalid solver request")
			}
			if n == 1 && (payload.Memories != "" || strings.Contains(payload.Problem, "future-background")) {
				t.Error("future state leaked to first step")
			}
			if n == 2 && (!strings.Contains(payload.Memories, "OWN_WRONG_SOLUTION_1") || !strings.Contains(payload.Memories, "first question") || !strings.Contains(payload.Problem, "future-background")) {
				t.Error("own prior wrong solution missing, or current background lost")
			}
			content = fmt.Sprintf("OWN_WRONG_SOLUTION_%d", n)
		} else {
			gradeCalls.Add(1)
			if !strings.Contains(request.Messages[1].Content, "REFERENCE_SECRET") {
				t.Error("judge missing reference")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	reader := LongMemEvalQAJudgeOptions{Model: "reader", BaseURL: server.URL, APIKey: "fixture", Timeout: time.Second}
	judge := reader
	judge.Model = "judge"
	task := &arenaTask{ID: 0, Paper: "paper", Questions: []string{"first question", "second question"}, Answers: []string{"REFERENCE_SECRET_1", "REFERENCE_SECRET_2"}, Backgrounds: []string{"initial-background", "future-background"}}
	job := &externalJob{ID: "synthetic", Dataset: "formal_reasoning_math", Mode: "memory", Arena: task}
	for i, q := range task.Questions {
		job.Answers = append(job.Answers, externalAnswer{Index: i, Question: q, Reference: task.Answers[i], Error: "not executed"})
	}
	cache := &externalFixtureEmbedding{}
	executeExternalJob(context.Background(), t, job, externalPlan{TopN: 5, ContextBytes: 64000, ArenaMaxTokens: 8192}, t.TempDir(), EmbeddingOptions{Mode: EmbeddingModeProvider, Model: "fixture"}, cache, PipelineRerankOptions{Model: "rerank_compatible:fixture", BaseURL: server.URL, Timeout: time.Second}, reader, judge)
	if readCalls.Load() != 2 || gradeCalls.Load() != 2 || job.Answers[0].Correct || job.Answers[1].Correct {
		t.Fatal("wrong chain or grade result", job)
	}
	for _, a := range job.Answers {
		if a.Error != "" || a.WriteError != "" || !a.Judged {
			t.Fatal("chain failed", a)
		}
	}
}

func TestExternalLongMemEvalKeepsAbstentionAndGoldOutOfReader(t *testing.T) {
	var reads, grades atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rerank" {
			var request struct {
				Documents []string `json:"documents"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			rows := make([]map[string]any, len(request.Documents))
			for i := range rows {
				rows[i] = map[string]any{"index": i, "relevance_score": 0.9}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
			return
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		answer := "yes"
		if request.Model == "reader" {
			reads.Add(1)
			wire, _ := json.Marshal(request)
			for _, secret := range []string{"REFERENCE_SECRET", "has_answer", "single-session-user", "selected_abs", "session-secret"} {
				if strings.Contains(string(wire), secret) {
					t.Error("offline annotation entered reader", secret)
				}
			}
			answer = "I do not know."
		} else {
			grades.Add(1)
			if !strings.Contains(request.Messages[1].Content, "REFERENCE_SECRET") || !strings.Contains(request.Messages[1].Content, "unanswerable question") {
				t.Error("abstention-specific judge rubric/reference missing")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "choices": []any{map[string]any{"message": map[string]string{"content": answer}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	reader := LongMemEvalQAJudgeOptions{Model: "reader", BaseURL: server.URL, APIKey: "fixture", Timeout: time.Second}
	judge := reader
	judge.Model = "judge"
	s := &longMemEvalSample{QuestionID: "selected_abs", QuestionType: "single-session-user", Question: "What is the missing birthday?", Answer: "REFERENCE_SECRET: not recorded", QuestionDate: "2024/01/03", HaystackSessionIDs: []any{"session-secret-1", "session-secret-2"}, HaystackDates: []any{"2024/01/01", "2024/01/02"}, HaystackSessions: [][]longMemEvalTurn{{{Role: "user", Content: "I went to Paris.", HasAnswer: true}}, {{Role: "assistant", Content: "Welcome back."}}}}
	for _, mode := range []string{"memory", "none"} {
		job := &externalJob{ID: s.QuestionID, Dataset: "longmemeval_s", Mode: mode, Type: s.QuestionType, LongMem: s, Answers: []externalAnswer{{Question: s.Question, Reference: normalizeAnyString(s.Answer), Error: "not executed"}}}
		executeExternalJob(context.Background(), t, job, externalPlan{TopN: 5, ContextBytes: 64000, LongMemMaxTokens: 4096}, t.TempDir(), EmbeddingOptions{Mode: EmbeddingModeProvider, Model: "fixture"}, &externalFixtureEmbedding{}, PipelineRerankOptions{Model: "rerank_compatible:fixture", BaseURL: server.URL, Timeout: time.Second}, reader, judge)
		a := job.Answers[0]
		if !a.Judged || !a.Correct || a.Error != "" || (mode == "none" && a.Context != "") || (mode == "memory" && !strings.Contains(a.Context, "Paris")) {
			t.Fatal("abstention case was omitted or failed", job)
		}
	}
	if reads.Load() != 2 || grades.Load() != 2 {
		t.Fatal("abstention/control denominator changed")
	}
}

func TestExternalContinuationPreservesFailuresAndRunsOnlyUnattemptedSteps(t *testing.T) {
	var reads, grades atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		text := "no"
		if body.Model == "reader" {
			reads.Add(1)
			var payload struct{ Problem, Memories string }
			_ = json.Unmarshal([]byte(body.Messages[1].Content), &payload)
			if !strings.Contains(payload.Problem, "fourth question") || !strings.Contains(payload.Memories, "OWN_WRONG_SOLUTION") || strings.Contains(payload.Memories, "PARTIAL_OUTPUT") || strings.Contains(payload.Memories, "REFERENCE_SECRET") {
				t.Error("continuation history or first-attempt boundary violated", payload)
			}
			text = "NEW_OWN_SOLUTION"
		} else {
			grades.Add(1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": body.Model, "choices": []any{map[string]any{"message": map[string]string{"content": text}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	reader := LongMemEvalQAJudgeOptions{Model: "reader", BaseURL: server.URL, APIKey: "fixture", Timeout: time.Second}
	judge := reader
	judge.Model = "judge"
	task := &arenaTask{ID: 0, Paper: "paper", Questions: []string{"first question", "second question", "third question", "fourth question"}, Answers: []string{"REFERENCE_SECRET_0", "REFERENCE_SECRET_1", "REFERENCE_SECRET_2", "REFERENCE_SECRET_3"}, Backgrounds: []string{"b0", "b1", "b2", "b3"}}
	job := &externalJob{ID: "checkpoint", Dataset: "formal_reasoning_math", Mode: "memory", Arena: task, Resume: true}
	for i, q := range task.Questions {
		job.Answers = append(job.Answers, externalAnswer{Index: i, Question: q, Reference: task.Answers[i], Error: "not executed"})
	}
	job.Answers[0].Answer, job.Answers[0].Error, job.Answers[0].Judged = "OWN_WRONG_SOLUTION", "", true
	job.Answers[1].Answer, job.Answers[1].Error = "PARTIAL_OUTPUT", "reader: output budget exhausted"
	job.Answers[2].Error = "attempt interrupted before completion"
	before := append([]externalAnswer{}, job.Answers[:2]...)
	executeExternalJob(context.Background(), t, job, externalPlan{TopN: 5, ContextBytes: 64000, ArenaMaxTokens: 8192}, t.TempDir(), EmbeddingOptions{Mode: EmbeddingModeProvider, Model: "fixture"}, &externalFixtureEmbedding{}, PipelineRerankOptions{Model: "rerank_compatible:fixture", BaseURL: server.URL, Timeout: time.Second}, reader, judge)
	if reads.Load() != 1 || grades.Load() != 1 || !reflect.DeepEqual(before, job.Answers[:2]) || job.Answers[2].Error != "execution interrupted before checkpoint; not retried" || job.Answers[3].Indexed != 1 || !job.Answers[3].Judged || job.Answers[3].Error != "" {
		t.Fatal("attempted results were retried/changed or state restore failed", job)
	}
}

func TestExternalContinuationRejectsChangedProtocolAndSealedCases(t *testing.T) {
	dir := t.TempDir()
	manifest := map[string]any{"plan_sha256": "frozen-plan", "reader_format": "plain_text_v1", "scope": "fixture", "code_revision": "parent-code"}
	job := &externalJob{ID: "checkpoint", Dataset: "formal_reasoning_math", Mode: "memory", Answers: []externalAnswer{{Index: 0, Question: "q0", Reference: "r0", Answer: "partial", Error: "reader: budget"}, {Index: 1, Question: "q1", Reference: "r1", Error: "not executed"}}}
	if err := writePipelineJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "checkpoint-memory.json")
	if err := writePipelineJSON(path, job); err != nil {
		t.Fatal(err)
	}
	m, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	raw, _ := os.ReadFile(path)
	seal := externalSeal{Protocol: "memstation.external.memory.v1", ManifestSHA256: pipelineHash(string(m)), Files: map[string]string{"checkpoint-memory.json": pipelineHash(string(raw))}, ProcessExit: 2, FinishedAt: "2026-10-27T00:00:00Z"}
	if err := writePipelineJSON(filepath.Join(dir, "sealed.json"), seal); err != nil {
		t.Fatal(err)
	}
	template := func() *externalJob {
		return &externalJob{ID: job.ID, Dataset: job.Dataset, Mode: job.Mode, Answers: []externalAnswer{{Index: 0, Question: "q0", Reference: "r0", Error: "not executed"}, {Index: 1, Question: "q1", Reference: "r1", Error: "not executed"}}}
	}
	loaded := template()
	if _, err := loadExternalContinuation(dir, []*externalJob{loaded}, manifest); err != nil || !loaded.Resume || loaded.Answers[0].Error != "reader: budget" {
		t.Fatal("failed outcome not preserved", err)
	}
	changed := map[string]any{"plan_sha256": "changed-plan", "reader_format": "plain_text_v1", "scope": "fixture"}
	if _, err := loadExternalContinuation(dir, []*externalJob{template()}, changed); err == nil {
		t.Fatal("changed experiment resumed")
	}
	job.Answers[0].Reference = "altered-reference"
	if err := writePipelineJSON(path, job); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExternalContinuation(dir, []*externalJob{template()}, manifest); err == nil {
		t.Fatal("unsealed case mutation accepted")
	}
	raw, _ = os.ReadFile(path)
	seal.Files["checkpoint-memory.json"] = pipelineHash(string(raw))
	if err := writePipelineJSON(filepath.Join(dir, "sealed.json"), seal); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExternalContinuation(dir, []*externalJob{template()}, manifest); err == nil {
		t.Fatal("sealed reference does not match source")
	}
}
