package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

func TestPipelineOracleBoundariesAndNoGoldLeakage(t *testing.T) {
	samples, err := loadPipelineSamples(pipelineFixture(t), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	s := samples[0]
	q := s.Questions[1]
	text, refs, err := pipelineControlContext(s, q, "gold_evidence", 4096, 1)
	if err != nil || len(refs) != 2 || strings.Contains(text, "changed my plan") || strings.Contains(text, "GOLD_SENTINEL") || !strings.Contains(text, "Speaker: Alex") {
		t.Fatal(text, refs, err)
	}
	full, refs, err := pipelineControlContext(s, q, "full_history", 4096, 0)
	if err != nil || len(refs) != 3 || !strings.Contains(full, "changed my plan") {
		t.Fatal(refs, err)
	}
	if _, _, err = pipelineControlContext(s, q, "full_history", 10, 0); err == nil {
		t.Fatal("silently truncated full history")
	}
	q.Evidence = append(q.Evidence, "D99:9")
	if _, _, err = pipelineControlContext(s, q, "gold_evidence", 4096, 1); err == nil {
		t.Fatal("accepted partial gold evidence")
	}
	if _, _, err = pipelineControlContext(s, s.Questions[0], "gold_evidence", 4096, 0); err == nil {
		t.Fatal("missing evidence ignored")
	}
	text, refs, err = pipelineControlContext(s, q, "question_only", 4096, 1)
	if err != nil || text != "" || len(refs) != 0 {
		t.Fatal("question-only leaked sources")
	}
}

func TestPipelineControlRunFreezesFailuresAndPinsProtocol(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 {
			t.Error("bad request")
			w.WriteHeader(400)
			return
		}
		output := `{"answer":"unknown"}`
		switch body.Messages[0].Content {
		case pipelineAnswerInstruction:
			if strings.Contains(body.Messages[1].Content, "GOLD_SENTINEL") {
				t.Error("gold answer leaked to reader")
			}
		case pipelineJudgeInstruction:
			// Schema failures must remain in the denominator and never be rerun on resume.
			output = `{"correct":null,"reason":"deliberate invalid grade"}`
		default:
			t.Error("control invoked ingestion or other prompts")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": output}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 4, "total_tokens": 16}})
	}))
	defer server.Close()
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "SECRET_SENTINEL"}
	o := LOCOMOPipelineOptions{DatasetPath: pipelineFixture(t), OutputDir: t.TempDir(), Prefix: "eval-controls", CodeRevision: "fixture", TopN: 5, ContextBytes: 4096, Modes: []string{"question_only", "full_history", "gold_evidence"}, Answerer: cfg, Judge: cfg}
	report, err := RunLOCOMOPipeline(context.Background(), o)
	if err == nil || len(report.Answers) != 6 || report.ExpectedSessions != 0 || report.Modes["gold_evidence"].Expected != 2 || report.Modes["gold_evidence"].Errors != 2 {
		t.Fatal("bad failure denominator", err, report.Modes)
	}
	if calls.Load() != 10 {
		t.Fatal("missing-evidence oracle called reader or unexpected calls", calls.Load())
	}
	for _, a := range report.Answers {
		if a.ContextHash != pipelineHash(a.Context) || !a.DiagnosticOnly {
			t.Fatal("missing context provenance")
		}
	}
	before := calls.Load()
	if _, err = RunLOCOMOPipeline(context.Background(), o); err == nil || calls.Load() != before {
		t.Fatal("resume retried failed model attempts", err, calls.Load())
	}
	raw, _ := os.ReadFile(filepath.Join(o.OutputDir, "manifest.json"))
	if strings.Contains(string(raw), "SECRET_SENTINEL") || report.Manifest["answer_prompt_sha256"] != pipelineHash(pipelineAnswerInstruction) {
		t.Fatal("unsafe/unpinned manifest")
	}
	changed := o
	changed.OracleNeighborTurns = 1
	if _, err = RunLOCOMOPipeline(context.Background(), changed); err == nil {
		t.Fatal("accepted changed oracle protocol")
	}
	checkpoint := filepath.Join(o.OutputDir, "answers", "synthetic.2-full_history.json")
	raw, _ = os.ReadFile(checkpoint)
	var a PipelineAnswer
	_ = json.Unmarshal(raw, &a)
	a.Context += "tamper"
	_ = writePipelineJSON(checkpoint, a)
	got, err := RunLOCOMOPipeline(context.Background(), o)
	if err == nil || calls.Load() != before {
		t.Fatal("tampered checkpoint rerun")
	}
	found := false
	for _, a := range got.Answers {
		if strings.Contains(a.Error, "digest mismatch") {
			found = true
		}
	}
	if !found {
		t.Fatal("context corruption not detected")
	}
}

func TestPipelineRerankerUsesProductionEngineAndReportsDegradation(t *testing.T) {
	var calls atomic.Int64
	malformed := atomic.Bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		var body struct {
			Documents []string `json:"documents"`
			Model     string   `json:"model"`
			TopN      int      `json:"top_n"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "test-model" || body.TopN != len(body.Documents) {
			t.Error("wrong rerank wire contract")
		}
		results := []any{}
		for i := range body.Documents {
			if malformed.Load() && i == 0 {
				continue
			}
			results = append(results, map[string]any{"index": i, "relevance_score": float64(i+1) / float64(len(body.Documents))})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "test"}
	o := LOCOMOPipelineOptions{Modes: []string{"question_only"}, Answerer: cfg, Judge: cfg, Rerank: PipelineRerankOptions{Model: "rerank_compatible:test-model", ModelRevision: "sha256:test", BaseURL: server.URL}}
	if err := normalizePipelineProtocol(&o); err != nil {
		t.Fatal(err)
	}
	repo := memorystore.NewInMemoryMemoryRepository()
	for _, id := range []string{"a", "b", "c"} {
		if err := repo.StoreDocument(context.Background(), domain.MemoryDocument{ID: id, NodeID: id, UserID: "u", Type: domain.MemoryTypeFact, Content: "bicycle " + id, Importance: 1}); err != nil {
			t.Fatal(err)
		}
	}
	engine := memoryservice.NewEngine(repo, nil)
	configurePipelineReranker(engine, o.Rerank)
	for _, broken := range []bool{false, true} {
		malformed.Store(broken)
		result, err := engine.SearchWithVariant(context.Background(), domain.SearchInput{UserID: "u", Query: "bicycle", TopN: 2}, memoryservice.SearchVariantFull)
		if err != nil {
			t.Fatal(err)
		}
		expected := "applied"
		if broken {
			expected = "degraded"
		}
		if result.Rerank.Status != expected {
			t.Fatal(result.Rerank)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("reranker never reached", calls.Load())
	}
}

func TestPipelineOutputLeaseAndOracleComparison(t *testing.T) {
	dir := t.TempDir()
	first, err := lockPipelineOutput(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, e := lockPipelineOutput(dir); e == nil {
		_ = second.Close()
		t.Fatal("concurrent output accepted")
	}
	_ = first.Close()
	resumed, err := lockPipelineOutput(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = resumed.Close()
	report := LOCOMOPipelineReport{Answers: []PipelineAnswer{
		{SampleID: "s", QuestionID: "1", Category: 1, Mode: "full_history", Correct: true, Judged: true},
		{SampleID: "s", QuestionID: "1", Category: 1, Mode: "raw", Correct: false, Judged: true},
		{SampleID: "s", QuestionID: "2", Category: 1, Mode: "full_history", Correct: false, Judged: true},
		{SampleID: "s", QuestionID: "2", Category: 1, Mode: "raw", Correct: true, Judged: true},
		{SampleID: "s", QuestionID: "3", Category: 2, Mode: "full_history", Correct: true, Judged: true},
		{SampleID: "s", QuestionID: "3", Category: 2, Mode: "raw", Correct: true, Judged: true, Error: "corrupt"},
	}}
	refreshPipelineSummaries(&report)
	v := report.OracleComparisons["raw_vs_full_history"]
	if v.Paired != 2 || v.ReferenceCorrect != 1 || v.BothCorrect != 0 || v.RetrievalOnlyCorrect != 1 || v.Retention == nil || *v.Retention != 0 {
		t.Fatal(v)
	}
	if report.BySample["s"]["raw"].Expected != 3 || report.ByCategory[2]["raw"].Correct != 0 {
		t.Fatal("failure removed from group denominator")
	}
}

func TestPipelineEvidenceOptionsRejectAmbiguousHydration(t *testing.T) {
	for _, o := range []LOCOMOPipelineOptions{
		{ContextFormat: "unknown"},
		{ContextFormat: "plain", SourceNeighborTurns: 1},
		{ContextFormat: "evidence", SourceNeighborTurns: -1},
		{ContextFormat: "evidence", SourceNeighborTurns: 3},
	} {
		if err := normalizePipelineProtocol(&o); err == nil || (!strings.Contains(err.Error(), "context format") && !strings.Contains(err.Error(), "source neighbors")) {
			t.Fatalf("invalid evidence options accepted: %+v %v", o, err)
		}
	}
}

func TestPipelineRawNeighborsBindCurrentIdentity(t *testing.T) {
	path := pipelineFixture(t)
	samples, err := loadPipelineSamples(path, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := LoadLOCOMODataset(path, LOCOMOOptions{SkipCategoryFive: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := newHarnessWithClient(context.Background(), dataset, EmbeddingOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var input struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil || len(input.Messages) != 2 {
			t.Error("invalid provider request")
			w.WriteHeader(400)
			return
		}
		output := `{"answer":"unknown"}`
		if input.Messages[0].Content == pipelineJudgeInstruction {
			output = `{"correct":false,"reason":"contract"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": output}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
	runtime := &pipelineRuntime{raw: raw, options: LOCOMOPipelineOptions{OutputDir: t.TempDir(), TopN: 5, ContextBytes: 4096, ContextFormat: "evidence", SourceNeighborTurns: 1, Answerer: cfg, Judge: cfg}}
	sample := samples[0]
	result := runtime.answer(context.Background(), "unused", sample, sample.Questions[0], "raw")
	if result.Error != "" || !result.Judged {
		t.Fatal("raw source-neighbor evaluation failed", result.Error)
	}
	user := locomoUserID(sample.ID, "")
	record, err := runtime.rawSources.GetConversation(context.Background(), user, user+"-"+sample.Sessions[0].ID)
	if err != nil || record.AgentID == "" || record.WorkspaceID != domain.PersonalWorkspaceID(user) {
		t.Fatal("source has no explicit current identity", err, record.AgentID, record.WorkspaceID)
	}
}
