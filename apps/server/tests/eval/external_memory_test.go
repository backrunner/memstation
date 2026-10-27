package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/provider"
	memoryservice "memstation/apps/server/internal/service/memory"
	rerankservice "memstation/apps/server/internal/service/rerank"
)

// This is an explicitly invoked external evaluation, never a default CI gate.
// Only the judge sees references/types; the arena memory receives own outputs,
// never reference solutions, grades, future questions or future backgrounds.
type externalSource struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	SHA256   string   `json:"sha256"`
	Revision string   `json:"revision"`
	IDs      []string `json:"question_ids"`
}

type externalPlan struct {
	ArenaBaselines        map[string]externalSource `json:"arena_baselines,omitempty"`
	ArenaBaselineManifest externalSource            `json:"arena_baseline_manifest,omitempty"`
	CreateArenaBaseline   bool                      `json:"create_arena_baseline,omitempty"`
	Scope                 string                    `json:"scope"`
	LongMemEval           *externalSource           `json:"longmemeval,omitempty"`
	Arena                 []externalSource          `json:"arena"`
	TopN                  int                       `json:"top_n"`
	ContextBytes          int                       `json:"context_bytes"`
	LongMemMaxTokens      int                       `json:"longmem_max_output_tokens"`
	ArenaMaxTokens        int                       `json:"arena_max_output_tokens"`
	Concurrency           int                       `json:"concurrency"`
	RerankTimeout         int                       `json:"rerank_timeout_seconds"`
	SourcePolicy          string                    `json:"source_policy"`
	ArenaAgent            string                    `json:"arena_agent"`
	JudgeInMemory         bool                      `json:"judge_signal_in_memory"`
	ReaderPolicy          string                    `json:"reader_policy"`
}

type arenaTask struct {
	ID          int      `json:"id"`
	Paper       string   `json:"paper_name"`
	Questions   []string `json:"questions"`
	Answers     []string `json:"answers"`
	Backgrounds []string `json:"backgrounds"`
}

func loadArenaTasks(raw []byte) ([]arenaTask, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	var tasks []arenaTask
	seen := map[int]bool{}
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var task arenaTask
		if err := json.Unmarshal(scanner.Bytes(), &task); err != nil {
			return nil, err
		}
		if seen[task.ID] || task.Paper == "" || len(task.Questions) == 0 || len(task.Answers) != len(task.Questions) || len(task.Backgrounds) != len(task.Questions) {
			return nil, fmt.Errorf("invalid or duplicate arena task %d", task.ID)
		}
		seen[task.ID] = true
		for i, q := range task.Questions {
			if strings.TrimSpace(q) == "" || strings.TrimSpace(task.Answers[i]) == "" {
				return nil, fmt.Errorf("empty arena question or reference in task %d", task.ID)
			}
		}
		tasks = append(tasks, task)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("empty arena dataset")
	}
	return tasks, nil
}

// Public background belongs to the current task only. It is never replaced by
// a reference solution or by a later subtask's background.
func arenaProblem(question, background string) string {
	if strings.Contains(question, "### BACKGROUND") || strings.Contains(question, "### PROBLEM") {
		return question
	}
	if background == "" {
		background = "No information provided."
	}
	return "### BACKGROUND:\n" + background + "\n### PROBLEM:\n" + question
}

func arenaExperience(question, answer string) string {
	return "## Task: " + question + "\n## solution: " + answer + "\n"
}

// Keep entire source records. Excluded/oversize records are reported, not
// silently truncated, and only request-local labels enter the reading prompt.
func externalContext(docs []domain.MemoryDocument, budget int) (string, []string, int) {
	var b strings.Builder
	var ids []string
	omitted := 0
	for _, doc := range docs {
		header := map[string]any{"source": fmt.Sprintf("source-%d", len(ids)+1)}
		if date, ok := doc.Metadata["session_date"]; ok {
			header["source_time"] = date
		}
		raw, _ := json.Marshal(header)
		text := string(raw) + "\n" + doc.Content + "\n\n"
		if b.Len()+len(text) > budget {
			omitted++
			continue
		}
		b.WriteString(text)
		ids = append(ids, doc.ID)
	}
	return b.String(), ids, omitted
}

// Cache only deterministic embedding computations, not search results, answers
// or grades. Content, configured model and pinned model revision identify a key.
type externalEmbeddingCache struct {
	delegate ports.EmbeddingClient
	dir      string
	model    string
	revision string
	locks    sync.Map
}

// Local cross-encoders may expose one inference slot. Bound their queue outside
// the provider timeout; otherwise waiting for unrelated jobs exhausts the
// request's inference budget before it starts. Cloud reading remains parallel.
var externalRerankGate = make(chan struct{}, 1)

type externalSerialReranker struct{ delegate ports.Reranker }

func (r externalSerialReranker) Rerank(ctx context.Context, in domain.RerankInput) ([]domain.RerankResult, error) {
	select {
	case externalRerankGate <- struct{}{}:
		defer func() { <-externalRerankGate }()
		return r.delegate.Rerank(ctx, in)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func configureExternalReranker(engine *memoryservice.Engine, o PipelineRerankOptions) {
	engine.SetRerankConfig(memoryservice.RerankConfig{Model: o.Model, CandidateSelection: o.CandidateSelection, CandidateMultiplier: o.CandidateMultiplier, MinCandidates: o.MinCandidates, MaxCandidates: o.MaxCandidates})
	p, _, _ := domain.ParseRerankModel(o.Model) // Validated before execution.
	engine.SetReranker(externalSerialReranker{delegate: rerankservice.NewService(provider.StaticConfigResolver{p: {BaseURL: o.BaseURL, APIKey: o.APIKey}}, o.Model, o.Timeout)})
}

func (c *externalEmbeddingCache) Embed(ctx context.Context, in domain.EmbeddingInput) (domain.EmbeddingResult, error) {
	if in.Model == "" {
		in.Model = c.model
	}
	key := pipelineHash(c.revision + "\x00" + in.Model + "\x00" + in.Text)
	lock, _ := c.locks.LoadOrStore(key, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	path := filepath.Join(c.dir, key+".json")
	var result domain.EmbeddingResult
	if raw, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(raw, &result); err != nil || !validExternalEmbedding(result) {
			return result, fmt.Errorf("invalid embedding cache entry")
		}
		return result, nil
	} else if !os.IsNotExist(err) {
		return result, err
	}
	result, err := c.delegate.Embed(ctx, in)
	if err != nil {
		return result, err
	}
	if !validExternalEmbedding(result) {
		return result, fmt.Errorf("invalid provider embedding")
	}
	if err = writePipelineJSON(path, result); err != nil {
		return result, err
	}
	return result, nil
}

func validExternalEmbedding(r domain.EmbeddingResult) bool {
	if r.Dimensions != len(r.Vector) || len(r.Vector) == 0 || r.Model == "" {
		return false
	}
	var norm float64
	for _, v := range r.Vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return false
		}
		norm += float64(v) * float64(v)
	}
	return norm > 0
}

type externalAnswer struct {
	Index           int                             `json:"index"`
	Question        string                          `json:"question"`
	Reference       string                          `json:"reference"`
	Answer          string                          `json:"answer"`
	Correct         bool                            `json:"correct"`
	Judged          bool                            `json:"judged"`
	Error           string                          `json:"error,omitempty"`
	Context         string                          `json:"context"`
	ContextHash     string                          `json:"context_sha256"`
	SourceIDs       []string                        `json:"source_ids"`
	Omitted         int                             `json:"omitted_context_records"`
	Indexed         int                             `json:"indexed_documents"`
	Search          *CaseReport                     `json:"search,omitempty"`
	Rerank          memoryservice.RerankDiagnostics `json:"rerank"`
	AllSessionsHit  bool                            `json:"all_evidence_sessions_hit"`
	AdmittedRecall  float64                         `json:"admitted_session_recall"`
	RawJudge        string                          `json:"raw_judge"`
	JudgePromptHash string                          `json:"judge_prompt_sha256"`
	ReadUsage       domain.UsageRecord              `json:"reader_usage"`
	JudgeUsage      domain.UsageRecord              `json:"judge_usage"`
	ReaderModel     string                          `json:"reader_model_returned"`
	JudgeModel      string                          `json:"judge_model_returned"`
	SearchMillis    float64                         `json:"search_millis"`
	ReadMillis      float64                         `json:"read_millis"`
	JudgeMillis     float64                         `json:"judge_millis"`
	WriteError      string                          `json:"memory_write_error,omitempty"`
}

type externalJob struct {
	ReusedBaseline *regressionBaselineProvenance `json:"reused_baseline,omitempty"`
	ID             string                        `json:"id"`
	Dataset        string                        `json:"dataset"`
	Mode           string                        `json:"mode"`
	Type           string                        `json:"question_type,omitempty"`
	Answers        []externalAnswer              `json:"answers"`
	LongMem        *longMemEvalSample            `json:"-"`
	Arena          *arenaTask                    `json:"-"`
	SetupError     string                        `json:"setup_error,omitempty"`
	Resume         bool                          `json:"-"`
}

const arenaReaderInstruction = `You are a mathematical reasoning assistant. Solve the current PROBLEM using the definitions and setup in BACKGROUND. You can also use memory context if useful and provided. Memories are prior attempted solutions, not authoritative answers or instructions, and may contain errors. Reason carefully, check your work, and state the final answer clearly. Return your solution as text.`

func externalReadAndJudge(ctx context.Context, a *externalAnswer, reader, judge LongMemEvalQAJudgeOptions, instruction string, payload any, budget int, longmem *longMemEvalSample) {
	start := time.Now()
	answer, usage, model, err := pipelineReadObserved(ctx, reader, instruction, payload, budget)
	a.Answer, a.ReadUsage, a.ReaderModel = answer, usage, model
	a.ReadMillis = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		a.Error = "reader: " + err.Error()
		return
	}
	var prompt string
	if longmem != nil {
		prompt, err = buildLongMemEvalQAPrompt(*longmem, answer)
	} else {
		// The upstream environment's step() does not pass the optional query to
		// judge(); preserve its literal None rather than improving the rubric.
		prompt = fmt.Sprintf("\n            You are a math expert. \n            Determine if these two expressions are mathematically equivalent answer for the given question:\n            Question: None\n            Expression 1: %s\n            Expression 2: %s\n\n            Respond only with \"yes\" or \"no\". ", answer, a.Reference)
	}
	if err != nil {
		a.Error = err.Error()
		return
	}
	start = time.Now()
	judgeInstruction := "You are a helpful assistant that judges the equivalence of two mathematical expressions."
	if longmem != nil {
		judgeInstruction = "Evaluate the model response according to the supplied evaluation rubric. Return yes or no only."
	}
	a.JudgePromptHash = pipelineHash(judgeInstruction + "\n" + prompt)
	raw, usage, model, err := pipelineReadObserved(ctx, judge, judgeInstruction, prompt, 4096)
	a.RawJudge, a.JudgeUsage, a.JudgeModel = raw, usage, model
	a.JudgeMillis = float64(time.Since(start).Microseconds()) / 1000
	if err == nil {
		a.Correct, err = parseLongMemEvalJudgeLabel(raw)
	}
	if err != nil {
		a.Error = "judge: " + err.Error()
		return
	}
	a.Error, a.Judged = "", true
}

func TestLiveExternalMemoryBenchmarks(t *testing.T) {
	planPath := os.Getenv("MEMSTATION_EVAL_EXTERNAL_PLAN")
	if planPath == "" {
		t.Skip("manual LongMemEval/MemoryArena provider-backed evaluation")
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan externalPlan
	if err = json.Unmarshal(raw, &plan); err != nil || plan.JudgeInMemory || plan.ContextBytes <= 0 || plan.TopN <= 0 || plan.Concurrency < 1 || plan.Concurrency > 8 || plan.RerankTimeout < 1 || plan.RerankTimeout > 600 || plan.LongMemMaxTokens <= 0 || plan.ArenaMaxTokens <= 0 || plan.ReaderPolicy != "interpretation" || plan.SourcePolicy != "original_full_sessions_and_own_past_question_solution_only" || plan.ArenaAgent != "single_call_no_external_tools" {
		t.Fatal("invalid external benchmark plan", err)
	}
	out, revision := os.Getenv("MEMSTATION_EVAL_EXTERNAL_OUT"), os.Getenv("MEMSTATION_EVAL_CODE_REVISION")
	if out == "" || revision == "" {
		t.Fatal("output and code revision required")
	}
	readSource := func(s externalSource) []byte {
		t.Helper()
		raw, err := os.ReadFile(s.Path)
		if err != nil || pipelineHash(string(raw)) != s.SHA256 || s.Revision == "" {
			t.Fatal("external source provenance mismatch", s.Name, err)
		}
		return raw
	}
	var samples []longMemEvalSample
	if plan.LongMemEval != nil {
		if err = json.Unmarshal(readSource(*plan.LongMemEval), &samples); err != nil {
			t.Fatal(err)
		}
	}
	selected := map[string]bool{}
	var selectedIDs []string
	if plan.LongMemEval != nil {
		selectedIDs = plan.LongMemEval.IDs
	}
	for _, id := range selectedIDs {
		if id == "" || id == "." || id == ".." || filepath.Base(id) != id || selected[id] {
			t.Fatal("invalid selected question IDs")
		}
		selected[id] = true
	}
	if plan.LongMemEval != nil && len(selected) == 0 {
		t.Fatal("no LongMemEval questions selected")
	}
	var jobs []*externalJob
	found := map[string]bool{}
	for _, s := range samples {
		if !selected[s.QuestionID] {
			continue
		}
		if found[s.QuestionID] || len(s.HaystackSessions) != len(s.HaystackSessionIDs) || len(s.HaystackDates) != len(s.HaystackSessions) || strings.TrimSpace(s.Question) == "" {
			t.Fatal("invalid selected LongMemEval sample")
		}
		if _, err := buildLongMemEvalQAPrompt(s, "validation"); err != nil {
			t.Fatal(err)
		}
		found[s.QuestionID] = true
		for _, mode := range []string{"memory", "none"} {
			jobs = append(jobs, &externalJob{ID: s.QuestionID, Dataset: "longmemeval_s", Mode: mode, Type: s.QuestionType, LongMem: &s, Answers: []externalAnswer{{Question: s.Question, Reference: normalizeAnyString(s.Answer), Error: "not executed"}}})
		}
	}
	if len(found) != len(selected) {
		t.Fatal("selected LongMemEval questions missing")
	}
	datasetNames := map[string]bool{}
	for _, source := range plan.Arena {
		if source.Name != "formal_reasoning_math" && source.Name != "formal_reasoning_phys" || datasetNames[source.Name] {
			t.Fatal("invalid arena domain")
		}
		datasetNames[source.Name] = true
		tasks, err := loadArenaTasks(readSource(source))
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			for _, mode := range []string{"memory", "none"} {
				job := &externalJob{ID: fmt.Sprintf("%s-%d", source.Name, task.ID), Dataset: source.Name, Mode: mode, Arena: &task}
				for i, q := range task.Questions {
					job.Answers = append(job.Answers, externalAnswer{Index: i, Question: q, Reference: task.Answers[i], Error: "not executed"})
				}
				jobs = append(jobs, job)
			}
		}
	}
	if len(jobs) == 0 {
		t.Fatal("no evaluation tracks selected")
	}
	reader, judge := pipelineReplayRoleConfig(t, "ANSWERER"), pipelineReplayRoleConfig(t, "JUDGE")
	if reader, err = normalizeLongMemEvalQAJudgeOptions(reader); err != nil {
		t.Fatal(err)
	}
	if judge, err = normalizeLongMemEvalQAJudgeOptions(judge); err != nil {
		t.Fatal(err)
	}
	if (reader.ReasoningEffort != "low" && reader.ReasoningEffort != "high" && reader.ReasoningEffort != "max") || reader.Temperature == nil || *reader.Temperature != 0 || judge.Temperature == nil || *judge.Temperature != 0 {
		t.Fatal("this protocol requires explicit supported reader thinking and explicit zero reader/judge temperature")
	}
	if err = applyExternalArenaBaselines(plan, jobs, reader); err != nil {
		t.Fatal(err)
	}
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
	rerank := PipelineRerankOptions{Model: os.Getenv("RERANK_MODEL"), ModelRevision: os.Getenv("RERANK_MODEL_REVISION"), BaseURL: os.Getenv("RERANK_BASE_URL"), APIKey: os.Getenv("RERANK_API_KEY"), Timeout: time.Duration(plan.RerankTimeout) * time.Second, CandidateSelection: "balanced", CandidateMultiplier: 6, MinCandidates: 20, MaxCandidates: 50}
	if _, _, err := domain.ParseRerankModel(rerank.Model); err != nil || rerank.BaseURL == "" || rerank.ModelRevision == "" {
		t.Fatal("pinned reranker required", err)
	}
	if err = os.Mkdir(out, 0o700); err != nil {
		t.Fatal("use a fresh complete output directory", err)
	}
	manifest := map[string]any{"plan": plan, "plan_sha256": pipelineHash(string(raw)), "code_revision": revision, "reader": reader.runInfo(), "judge": judge.runInfo(), "embedding": embedding.runInfo(), "embedding_revision": cache.revision, "rerank": rerank.manifest(), "reader_format": "plain_text_v1", "source_ingestion": "original-record RAG via production MemoryEngine and VectorStore; no summary/fact workers", "storage": "isolated in-memory repositories; real provider embeddings and cross-encoder", "arena_agent_prompt_sha256": pipelineHash(arenaReaderInstruction), "longmemeval_reader_prompt_sha256": pipelineHash(pipelineInterpretationInstruction), "judge_policy": "LongMemEval task-specific upstream rubrics; MemoryArena upstream equivalence prompt with literal None query; strict yes/no output validation; no judge signal in memory", "scope": plan.Scope}
	manifest["rerank_max_inflight"] = 1
	if parent := os.Getenv("MEMSTATION_EVAL_EXTERNAL_PARENT"); parent != "" {
		continuation, err := loadExternalContinuation(parent, jobs, manifest)
		if err != nil {
			t.Fatal("invalid sealed continuation", err)
		}
		manifest["continuation"] = continuation
	}
	if err = writePipelineJSON(filepath.Join(out, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if err = writePipelineJSON(filepath.Join(out, job.ID+"-"+job.Mode+".json"), job); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	queue := make(chan *externalJob)
	for range plan.Concurrency {
		wg.Go(func() {
			for job := range queue {
				if job.ReusedBaseline != nil {
					continue
				}
				if ctx.Err() != nil {
					continue
				}
				executeExternalJob(ctx, t, job, plan, out, embedding, cache, rerank, reader, judge)
				for _, a := range job.Answers {
					if strings.Contains(a.Error, "connect: connection refused") {
						cancel()
					}
				}
			}
		})
	}
	for _, job := range jobs {
		queue <- job
	}
	close(queue)
	wg.Wait()
	if err = writePipelineJSON(filepath.Join(out, "completion.json"), map[string]any{"jobs": len(jobs), "completed_at": time.Now().UTC(), "cancelled": ctx.Err() != nil}); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("external dependency circuit open; unexecuted rows retained")
	}
}

func executeExternalJob(ctx context.Context, t *testing.T, job *externalJob, plan externalPlan, out string, embedding EmbeddingOptions, cache ports.EmbeddingClient, rerank PipelineRerankOptions, reader, judge LongMemEvalQAJudgeOptions) {
	t.Helper()
	path := filepath.Join(out, job.ID+"-"+job.Mode+".json")
	save := func() {
		if err := writePipelineJSON(path, job); err != nil {
			t.Error(err)
		}
	}
	if job.Resume {
		pending := false
		for i := range job.Answers {
			a := &job.Answers[i]
			pending = pending || a.Error == "not executed"
			if a.Error == "attempt interrupted before completion" {
				a.Error = "execution interrupted before checkpoint; not retried"
			}
		}
		if !pending {
			save()
			return
		}
	}
	owner := "eval-external-" + pipelineHash(job.ID + job.Mode)[:16]
	dataset := Dataset{Name: job.Dataset}
	if job.LongMem != nil && job.Mode == "memory" {
		dataset.Documents, _ = buildLongMemEvalSessionDocuments(*job.LongMem, owner)
		for i := range dataset.Documents {
			doc := &dataset.Documents[i]
			idx := doc.Metadata["session_index"].(int) - 1
			doc.Metadata = map[string]any{"session_date": normalizeAnyString(job.LongMem.HaystackDates[idx])}
		}
	}
	var bench *harness
	if job.Mode == "memory" {
		var err error
		bench, err = newHarnessWithClient(ctx, dataset, embedding, cache)
		if err != nil {
			job.SetupError = err.Error()
			for i := range job.Answers {
				job.Answers[i].Error = "index: " + err.Error()
			}
			save()
			return
		}
		configureExternalReranker(bench.engine, rerank)
		if job.LongMem != nil {
			bench.engine.SetClock(func() time.Time {
				return longMemEvalTimeOrDefault(job.LongMem.QuestionDate, datasetReferenceTime(dataset))
			})
		}
	}
	indexed := len(dataset.Documents)
	for i := range job.Answers {
		a := &job.Answers[i]
		if ctx.Err() != nil {
			return
		}
		if job.Resume && a.Error != "not executed" {
			// Restore only completed own solutions. Grades never decide writes;
			// partial readers and interrupted calls are not retried or indexed.
			if bench != nil && job.Arena != nil && a.Answer != "" && !strings.HasPrefix(a.Error, "reader:") && a.Error != "execution interrupted before checkpoint; not retried" && a.WriteError == "" {
				if err := storeExternalExperience(ctx, bench, owner, embedding.Model, i, a); err != nil {
					job.SetupError = "checkpoint memory restore: " + err.Error()
					for j := i + 1; j < len(job.Answers); j++ {
						if job.Answers[j].Error == "not executed" {
							job.Answers[j].Error = "not executed: checkpoint memory restore failed"
						}
					}
					save()
					return
				}
				indexed++
			}
			continue
		}
		a.Error, a.Indexed = "attempt interrupted before completion", indexed
		save()
		if bench != nil && indexed > 0 {
			start := time.Now()
			found, err := bench.engine.SearchWithVariant(ctx, domain.SearchInput{UserID: owner, Query: a.Question, TopN: plan.TopN, MemoryTypes: []domain.MemoryType{domain.MemoryTypeSegment}, IncludeSegments: true}, memoryservice.SearchVariantFull)
			a.SearchMillis = float64(time.Since(start).Microseconds()) / 1000
			if err == nil {
				err = bench.embeddingError()
			}
			a.Rerank = found.Rerank
			if err == nil && found.Rerank.Status != "applied" && (found.Rerank.Status != "skipped" || found.Rerank.Reason != "insufficient_candidates") {
				err = fmt.Errorf("reranker not applied: %s", found.Rerank.Status)
			}
			if err != nil {
				a.Error = "retrieval: " + err.Error()
				save()
				continue
			}
			a.Context, a.SourceIDs, a.Omitted = externalContext(found.Result.Memories, plan.ContextBytes)
			if job.LongMem != nil && !strings.HasSuffix(job.ID, "_abs") {
				_, positives := buildLongMemEvalSessionDocuments(*job.LongMem, owner)
				q := QueryCase{CaseID: job.ID, UserID: owner, Query: a.Question, Positives: positives}
				results := rankResults(q, found)
				a.Search = &CaseReport{CaseID: job.ID, UserID: owner, Query: a.Question, TopN: plan.TopN, Variant: memoryservice.SearchVariantFull, Rerank: found.Rerank, Results: results, Metrics: computeCaseMetrics(q, results)}
				admitted, hits := asSet(a.SourceIDs), 0
				for _, id := range positives {
					if _, ok := admitted[id]; ok {
						hits++
					}
				}
				a.AllSessionsHit = len(a.Search.Metrics.MissingPositiveIDs) == 0 && len(positives) > 0
				if len(positives) > 0 {
					a.AdmittedRecall = float64(hits) / float64(len(positives))
				}
			}
		}
		a.ContextHash = pipelineHash(a.Context)
		instruction, payload, budget := pipelineInterpretationInstruction, any(map[string]any{"question": a.Question, "question_date": normalizeAnyString(jobDate(job)), "memories": a.Context}), plan.LongMemMaxTokens
		if job.Arena != nil {
			instruction, budget = arenaReaderInstruction, plan.ArenaMaxTokens
			payload = map[string]any{"problem": arenaProblem(a.Question, job.Arena.Backgrounds[i]), "memories": a.Context}
		}
		externalReadAndJudge(ctx, a, reader, judge, instruction, payload, budget, job.LongMem)
		// Grading outcome never determines whether a completed own solution is
		// remembered. Even a wrong answer becomes part of the agent's history.
		if bench != nil && job.Arena != nil && a.Answer != "" && !strings.HasPrefix(a.Error, "reader:") {
			if err := storeExternalExperience(ctx, bench, owner, embedding.Model, i, a); err != nil {
				a.WriteError = err.Error()
			} else {
				indexed++
			}
			if a.WriteError != "" {
				for j := i + 1; j < len(job.Answers); j++ {
					job.Answers[j].Error = "not executed: prior memory write failed"
				}
				save()
				return
			}
		}
		save()
		t.Logf("%s %s %d/%d correct=%t judged=%t error=%s", job.ID, job.Mode, i+1, len(job.Answers), a.Correct, a.Judged, a.Error)
		if errors.Is(ctx.Err(), context.Canceled) || strings.Contains(a.Error, syscall.ECONNREFUSED.Error()) {
			return
		}
	}
}

func storeExternalExperience(ctx context.Context, bench *harness, owner, model string, index int, a *externalAnswer) error {
	doc := DatasetDocument{ID: fmt.Sprintf("experience-%d", index), NodeID: fmt.Sprintf("node-%d", index), UserID: owner, Type: domain.MemoryTypeSegment, Content: arenaExperience(a.Question, a.Answer), CreatedAt: time.Date(2026, 10, 27, 0, 0, index, 0, time.UTC)}.ToDomain()
	if err := bench.repo.StoreDocument(ctx, doc); err != nil {
		return err
	}
	return bench.vectors.Upsert(ctx, domain.VectorUpsertRequest{UserID: owner, GenerationID: memorygenCurrentGenerationID(owner), NodeID: doc.NodeID, NodeType: string(doc.Type), EmbeddingModel: model, EmbeddingVersion: 1, Content: doc.Content})
}

func jobDate(job *externalJob) any {
	if job.LongMem != nil {
		return job.LongMem.QuestionDate
	}
	return ""
}
