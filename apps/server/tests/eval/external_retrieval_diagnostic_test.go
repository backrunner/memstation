package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

// retrievalDiagnosticArm is one declared retrieval configuration. Arms share the
// external runner's engine path, reranker wiring and whole-record packing.
type retrievalDiagnosticArm struct {
	Name                string `json:"name"`
	Granularity         string `json:"granularity"`
	TopN                int    `json:"top_n"`
	ContextBytes        int    `json:"context_bytes"`
	RerankMaxCandidates int    `json:"rerank_max_candidates"`
	RerankMultiplier    int    `json:"rerank_candidate_multiplier"`
}

type retrievalDiagnosticPlan struct {
	DatasetPath   string                   `json:"dataset_path"`
	DatasetSHA256 string                   `json:"dataset_sha256"`
	QuestionIDs   []string                 `json:"question_ids"`
	Arms          []retrievalDiagnosticArm `json:"arms"`
	Concurrency   int                      `json:"concurrency"`
	RerankTimeout int                      `json:"rerank_timeout_seconds"`
}

type retrievalDiagnosticRow struct {
	Arm                    string  `json:"arm"`
	QuestionID             string  `json:"question_id"`
	QuestionType           string  `json:"question_type"`
	EvidenceSessions       int     `json:"evidence_sessions"`
	EvidenceSessionsPacked int     `json:"evidence_sessions_packed"`
	EvidenceRecords        int     `json:"evidence_records"`
	EvidenceRecordsPacked  int     `json:"evidence_records_packed"`
	RecordsPacked          int     `json:"records_packed"`
	RecordsOmitted         int     `json:"records_omitted"`
	ContextBytes           int     `json:"context_bytes"`
	Indexed                int     `json:"indexed"`
	RerankStatus           string  `json:"rerank_status"`
	SearchMillis           float64 `json:"search_millis"`
	Error                  string  `json:"error,omitempty"`
}

func validateRetrievalDiagnosticPlan(p retrievalDiagnosticPlan) error {
	if p.DatasetPath == "" || len(p.DatasetSHA256) != 64 || len(p.QuestionIDs) == 0 || len(p.Arms) == 0 || p.Concurrency < 1 || p.Concurrency > 8 || p.RerankTimeout < 1 || p.RerankTimeout > 600 {
		return fmt.Errorf("incomplete retrieval diagnostic plan")
	}
	seen := map[string]bool{}
	for _, arm := range p.Arms {
		if _, err := normalizeLongMemEvalOptions(LongMemEvalOptions{Granularity: LongMemEvalGranularity(arm.Granularity)}); err != nil || arm.Granularity == "" {
			return fmt.Errorf("arm %q: unsupported granularity", arm.Name)
		}
		if arm.Name == "" || seen[arm.Name] || arm.TopN < 1 || arm.ContextBytes < 1 || arm.RerankMaxCandidates < arm.TopN || arm.RerankMaxCandidates > 200 || arm.RerankMultiplier < 1 || arm.RerankMultiplier > 16 {
			return fmt.Errorf("arm %q: invalid bounds", arm.Name)
		}
		seen[arm.Name] = true
	}
	return nil
}

// This manual diagnostic measures how much annotated evidence survives retrieval
// and context packing under declared arms. Labels are used only for reporting,
// and no reader or judge is called.
func TestLiveLongMemEvalRetrievalDiagnostic(t *testing.T) {
	planPath := os.Getenv("MEMSTATION_EVAL_RETRIEVAL_DIAG_PLAN")
	if planPath == "" {
		t.Skip("manual retrieval diagnostic")
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan retrievalDiagnosticPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if err = validateRetrievalDiagnosticPlan(plan); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(plan.DatasetPath)
	if err != nil || pipelineHash(string(data)) != plan.DatasetSHA256 {
		t.Fatal("dataset fingerprint mismatch", err)
	}
	var samples []longMemEvalSample
	if err = json.Unmarshal(data, &samples); err != nil {
		t.Fatal(err)
	}
	byID := map[string]longMemEvalSample{}
	for _, s := range samples {
		byID[s.QuestionID] = s
	}
	out := os.Getenv("MEMSTATION_EVAL_RETRIEVAL_DIAG_OUT")
	f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal("fresh output file required", err)
	}
	_ = f.Close()
	embedding, err := normalizeEmbeddingOptions(EmbeddingOptions{Mode: EmbeddingModeProvider, Model: os.Getenv("EMBEDDING_MODEL"), BaseURL: os.Getenv("EMBEDDING_BASE_URL"), APIKey: os.Getenv("EMBEDDING_API_KEY"), Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	client, err := newEvalEmbeddingClient(embedding)
	if err != nil {
		t.Fatal(err)
	}
	cache := &externalEmbeddingCache{delegate: client, dir: os.Getenv("MEMSTATION_EVAL_EXTERNAL_CACHE"), model: embedding.Model, revision: os.Getenv("MEMSTATION_EVAL_EMBEDDING_REVISION")}
	if cache.dir == "" || cache.revision == "" {
		t.Fatal("private content cache and model revision required")
	}
	if err = os.MkdirAll(cache.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	type job struct {
		arm    retrievalDiagnosticArm
		sample longMemEvalSample
	}
	var jobs []job
	for _, id := range plan.QuestionIDs {
		s, ok := byID[id]
		if !ok {
			t.Fatal("unknown question", id)
		}
		if len(s.HaystackDates) != len(s.HaystackSessions) || len(s.HaystackSessionIDs) != len(s.HaystackSessions) {
			t.Fatal("LongMemEval session dates and IDs must match the sessions", id)
		}
		if shouldSkipLongMemEvalSample(s) {
			continue // abstention has no evidence to measure
		}
		for _, arm := range plan.Arms {
			jobs = append(jobs, job{arm, s})
		}
	}
	report := struct {
		PlanSHA256 string                   `json:"plan_sha256"`
		Embedding  any                      `json:"embedding"`
		Rows       []retrievalDiagnosticRow `json:"rows"`
	}{PlanSHA256: pipelineHash(string(raw)), Embedding: embedding.runInfo()}
	var mu sync.Mutex
	save := func() {
		mu.Lock()
		defer mu.Unlock()
		if e := writePipelineJSON(out, report); e != nil {
			t.Error(e)
		}
	}
	slots := make(chan struct{}, plan.Concurrency)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		slots <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-slots }()
			row := runRetrievalDiagnostic(context.Background(), j.arm, j.sample, plan.RerankTimeout, embedding, cache)
			mu.Lock()
			report.Rows = append(report.Rows, row)
			n := len(report.Rows)
			mu.Unlock()
			save()
			t.Logf("%d/%d %s %s evidence %d/%d records %d/%d error=%s", n, len(jobs), row.Arm, row.QuestionID, row.EvidenceSessionsPacked, row.EvidenceSessions, row.EvidenceRecordsPacked, row.EvidenceRecords, row.Error)
		}(j)
	}
	wg.Wait()
	save()
}

func runRetrievalDiagnostic(ctx context.Context, arm retrievalDiagnosticArm, s longMemEvalSample, rerankTimeout int, embedding EmbeddingOptions, cache *externalEmbeddingCache) retrievalDiagnosticRow {
	row := retrievalDiagnosticRow{Arm: arm.Name, QuestionID: s.QuestionID, QuestionType: s.QuestionType}
	owner := "eval-retrieval-" + pipelineHash(arm.Name + s.QuestionID)[:16]
	docs, positives, err := buildLongMemEvalDocuments(s, owner, LongMemEvalOptions{Granularity: LongMemEvalGranularity(arm.Granularity)})
	if err != nil {
		row.Error = err.Error()
		return row
	}
	sessionOf := map[string]string{}
	for i := range docs {
		doc := &docs[i]
		idx := doc.Metadata["session_index"].(int) - 1
		sessionOf[doc.ID] = longMemEvalSessionID(s, idx)
		// Same scrubbing as the external runner: only the session date reaches the index.
		doc.Metadata = map[string]any{"session_date": normalizeAnyString(s.HaystackDates[idx])}
	}
	bench, err := newHarnessWithClient(ctx, Dataset{Name: "longmemeval-retrieval-diagnostic", Documents: docs}, embedding, cache)
	if err != nil {
		row.Error = "index: " + err.Error()
		return row
	}
	configureExternalReranker(bench.engine, PipelineRerankOptions{Model: os.Getenv("RERANK_MODEL"), ModelRevision: os.Getenv("RERANK_MODEL_REVISION"), BaseURL: os.Getenv("RERANK_BASE_URL"), APIKey: os.Getenv("RERANK_API_KEY"), Timeout: time.Duration(rerankTimeout) * time.Second, CandidateSelection: "balanced", CandidateMultiplier: arm.RerankMultiplier, MinCandidates: min(20, arm.RerankMaxCandidates), MaxCandidates: arm.RerankMaxCandidates})
	bench.engine.SetClock(func() time.Time {
		return longMemEvalTimeOrDefault(s.QuestionDate, time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	})
	row.Indexed = len(docs)
	start := time.Now()
	found, err := bench.engine.SearchWithVariant(ctx, domain.SearchInput{UserID: owner, Query: s.Question, TopN: arm.TopN, MemoryTypes: []domain.MemoryType{domain.MemoryTypeSegment}, IncludeSegments: true}, memoryservice.SearchVariantFull)
	row.SearchMillis = float64(time.Since(start).Microseconds()) / 1000
	if err == nil {
		err = bench.embeddingError()
	}
	row.RerankStatus = found.Rerank.Status
	if err != nil {
		row.Error = "retrieval: " + err.Error()
		return row
	}
	contextText, admitted, omitted := externalContext(found.Result.Memories, arm.ContextBytes)
	row.ContextBytes, row.RecordsPacked, row.RecordsOmitted = len(contextText), len(admitted), omitted
	// Labels are consulted only now, after packing.
	evidence := longMemEvalStringSet(s.AnswerSessionIDs)
	packedSessions := map[string]bool{}
	for _, id := range admitted {
		packedSessions[sessionOf[id]] = true
	}
	row.EvidenceSessions = len(evidence)
	for id := range evidence {
		if packedSessions[id] {
			row.EvidenceSessionsPacked++
		}
	}
	packed := asSet(admitted)
	row.EvidenceRecords = len(positives)
	for _, id := range positives {
		if _, ok := packed[id]; ok {
			row.EvidenceRecordsPacked++
		}
	}
	return row
}

func TestRetrievalDiagnosticPlanValidation(t *testing.T) {
	valid := retrievalDiagnosticPlan{DatasetPath: "d.json", DatasetSHA256: pipelineHash("x"), QuestionIDs: []string{"q"}, Concurrency: 2, RerankTimeout: 300, Arms: []retrievalDiagnosticArm{{Name: "session", Granularity: "session", TopN: 10, ContextBytes: 128000, RerankMaxCandidates: 50, RerankMultiplier: 6}, {Name: "round", Granularity: "round", TopN: 40, ContextBytes: 128000, RerankMaxCandidates: 150, RerankMultiplier: 8}}}
	if err := validateRetrievalDiagnosticPlan(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*retrievalDiagnosticPlan){
		"unknown granularity": func(p *retrievalDiagnosticPlan) { p.Arms[0].Granularity = "paragraph" },
		"empty granularity":   func(p *retrievalDiagnosticPlan) { p.Arms[0].Granularity = "" },
		"duplicate arm":       func(p *retrievalDiagnosticPlan) { p.Arms[1].Name = p.Arms[0].Name },
		"pool below top n":    func(p *retrievalDiagnosticPlan) { p.Arms[1].RerankMaxCandidates = 10 },
		"no questions":        func(p *retrievalDiagnosticPlan) { p.QuestionIDs = nil },
		"bad digest":          func(p *retrievalDiagnosticPlan) { p.DatasetSHA256 = "short" },
	} {
		t.Run(name, func(t *testing.T) {
			p := valid
			p.Arms = append([]retrievalDiagnosticArm(nil), valid.Arms...)
			mutate(&p)
			if validateRetrievalDiagnosticPlan(p) == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}
