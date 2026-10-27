package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/security"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/memoryllm"
)

type regressionOutcome struct {
	ReusedBaseline  *regressionBaselineProvenance     `json:"reused_baseline,omitempty"`
	Dataset         string                            `json:"dataset"`
	Group           string                            `json:"group"`
	QuestionID      string                            `json:"question_id"`
	Mode            string                            `json:"mode"`
	PriorStatus     string                            `json:"prior_status"`
	Answer          externalAnswer                    `json:"answer"`
	Timings         *memoryservice.SearchTimings      `json:"search_timings,omitempty"`
	Evidence        memoryservice.EvidenceDiagnostics `json:"evidence"`
	EvidenceMillis  float64                           `json:"evidence_millis"`
	Retrieval       *memoryservice.DebugSearchResult  `json:"retrieval,omitempty"`
	Recall          *regressionSourceRecall           `json:"source_recall,omitempty"`
	WorkerDocuments map[string]int                    `json:"worker_documents,omitempty"`
}

type regressionObservedAdapter struct {
	ports.ProviderAdapter
	path string
	mu   sync.Mutex
}

func (a *regressionObservedAdapter) Complete(ctx context.Context, req domain.UnifiedRequest) (*domain.UnifiedResponse, error) {
	start := time.Now()
	response, err := a.ProviderAdapter.Complete(ctx, req)
	record := map[string]any{"user_id": req.UserID, "started_at": start.UTC(), "millis": float64(time.Since(start).Microseconds()) / 1000, "max_output_tokens": req.MaxOutputTokens, "reasoning_effort": req.ReasoningEffort}
	if response != nil {
		record["response"] = response
	}
	if err != nil {
		record["error"] = err.Error()
	}
	raw, _ := json.Marshal(record)
	a.mu.Lock()
	defer a.mu.Unlock()
	f, writeErr := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if writeErr != nil {
		return nil, writeErr
	}
	defer f.Close()
	if _, writeErr = f.Write(append(raw, '\n')); writeErr != nil {
		return nil, writeErr
	}
	return response, err
}

func regressionRole(t *testing.T, role string, limit int, thinking string) LongMemEvalQAJudgeOptions {
	t.Helper()
	cfg := pipelineReplayRoleConfig(t, role)
	cfg.Timeout = 10 * time.Minute
	cfg.MaxOutputTokens = limit
	if thinking != "" {
		cfg.ReasoningEffort = thinking
	}
	var err error
	cfg, err = normalizeLongMemEvalQAJudgeOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0 {
		t.Fatal("explicit zero temperature required")
	}
	return cfg
}

func regressionOutcomePath(out string, g regressionGroup, q regressionQuestion, mode string) string {
	return filepath.Join(out, "answers", pipelineHash(g.ID+"\x00"+q.ID+"\x00"+mode)+".json")
}

func TestLiveMemoryRegression(t *testing.T) {
	planPath := os.Getenv("MEMSTATION_EVAL_REGRESSION_PLAN")
	if planPath == "" {
		t.Skip("manual real-worker failure-cohort evaluation")
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan regressionPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	groups, err := loadRegressionGroups(plan)
	if err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("MEMSTATION_EVAL_REGRESSION_OUT")
	revision := os.Getenv("MEMSTATION_EVAL_CODE_REVISION")
	dbURL := os.Getenv("MEMSTATION_EVAL_DATABASE_URL")
	if out == "" || revision == "" || dbURL == "" {
		t.Fatal("fresh private output, code revision and dedicated database required")
	}
	if err = os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(out, "answers"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(out, "worker"), 0o700); err != nil {
		t.Fatal(err)
	}
	extraction := regressionRole(t, "EXTRACTION", plan.MaxTokens, plan.WorkerThinking)
	reader := regressionRole(t, "ANSWERER", plan.MaxTokens, "")
	reader.ReasoningEffort = "" // Each dataset has an explicit effective policy in the plan.
	judge := regressionRole(t, "JUDGE", plan.MaxTokens, "")
	baselines, err := loadRegressionArenaBaselines(plan, groups, reader)
	if err != nil {
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
		t.Fatal("pinned content-only embedding cache required")
	}
	if err = os.MkdirAll(cache.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rerank := PipelineRerankOptions{Model: os.Getenv("RERANK_MODEL"), ModelRevision: os.Getenv("RERANK_MODEL_REVISION"), BaseURL: os.Getenv("RERANK_BASE_URL"), APIKey: os.Getenv("RERANK_API_KEY"), Timeout: 3 * time.Minute, CandidateSelection: "balanced", CandidateMultiplier: 6, MinCandidates: 20, MaxCandidates: plan.RerankCandidates}
	if _, _, err = domain.ParseRerankModel(rerank.Model); err != nil || rerank.ModelRevision == "" || rerank.BaseURL == "" {
		t.Fatal("pinned local reranker required", err)
	}
	prefix := "eval-regression-" + pipelineHash(string(raw) + revision)[:12]
	manifest := map[string]any{"plan": plan, "plan_sha256": pipelineHash(string(raw)), "code_revision": revision, "reader": reader.runInfo(), "reader_thinking_by_dataset": plan.ReaderThinking, "extraction": extraction.runInfo(), "judge": judge.runInfo(), "embedding": embedding.runInfo(), "embedding_revision": cache.revision, "rerank": rerank.manifest(), "worker_concurrency": plan.Concurrency, "worker_output_policy": fmt.Sprintf("min %d, one explicit truncation retry to %d", memoryllm.DefaultMinOutputTokens, plan.MaxTokens), "reader_prompt_sha256": pipelineHash(pipelineInterpretationInstruction), "arena_prompt_sha256": pipelineHash(arenaReaderInstruction), "ingestion": "fresh PostgreSQL + actual finalization/outbox relay/worker handlers; durable local queue, no Redis/HTTP scheduling transport measurement", "scope": "fixed development failures and controls; no full-benchmark/held-out/SOTA claim; same-family judge", "source_policy": "all original source sessions; independent Arena histories per mode; own complete solutions regardless of grade; no gold/type/query-directed ingestion"}
	if err = writePipelineJSON(filepath.Join(out, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	// Reserve every expected answer before any model or database work.
	for _, g := range groups {
		for _, q := range g.Questions {
			for _, mode := range plan.Modes {
				a := regressionOutcome{Dataset: g.Dataset, Group: g.ID, QuestionID: q.ID, Mode: mode, PriorStatus: q.Prior, Answer: externalAnswer{Index: q.Index, Question: q.Query, Reference: q.Reference, Error: "not executed"}}
				if err = writePipelineJSON(regressionOutcomePath(out, g, q, mode), a); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	observed := &regressionObservedAdapter{ProviderAdapter: pipelineAdapter(extraction), path: filepath.Join(out, "extraction-calls.jsonl")}
	options := LOCOMOPipelineOptions{TaskConcurrency: plan.Concurrency, DatasetPath: plan.Locomo.Path, DatabaseURL: dbURL, OutputDir: filepath.Join(out, "worker"), Prefix: prefix, Modes: []string{"derived", "combined"}, TopN: plan.TopN, ContextBytes: plan.ContextBytes, CodeRevision: revision, EmbeddingRevision: cache.revision, Embedding: embedding, Extraction: extraction, Answerer: reader, Judge: judge, Rerank: rerank, ReaderPolicy: "interpretation", AnswerOutputTokens: plan.ReaderTokens, extractionAdapter: observed, manifestHash: pipelineHash(string(raw) + revision)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime, err := newPipelineRuntime(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.close()
	runtime.observed.EmbeddingClient = cache
	rawLocomo, err := LoadLOCOMODataset(plan.Locomo.Path, LOCOMOOptions{SkipCategoryFive: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	queue := make(chan regressionGroup)
	for range plan.Concurrency {
		wg.Go(func() {
			for g := range queue {
				runRegressionGroup(ctx, t, runtime, g, plan, out, prefix, embedding, cache, rerank, reader, judge, rawLocomo, baselines)
			}
		})
	}
	for _, g := range groups {
		queue <- g
	}
	close(queue)
	wg.Wait()
	if err = writePipelineJSON(filepath.Join(out, "completion.json"), map[string]any{"completed_at": time.Now().UTC(), "expected_answers": plan.ExpectedAnswers, "groups": len(groups), "cancelled": ctx.Err() != nil}); err != nil {
		t.Fatal(err)
	}
}

func regressionRawDataset(g regressionGroup, owner string, locomo Dataset) Dataset {
	data := Dataset{Name: "regression-" + g.Dataset}
	if g.LongMem != nil {
		data.Documents, _ = buildLongMemEvalSessionDocuments(*g.LongMem, owner)
		for i := range data.Documents {
			doc := &data.Documents[i]
			idx := doc.Metadata["session_index"].(int) - 1
			doc.Metadata = map[string]any{"session_date": normalizeAnyString(g.LongMem.HaystackDates[idx])}
			doc.ConversationID = owner + "-" + fmt.Sprintf("session-%03d", idx+1)
		}
	} else if g.Locomo != nil {
		for _, d := range locomo.Documents {
			if d.UserID == locomoUserID(g.Locomo.ID, "") {
				d.UserID = owner
				d.GenerationID = ""
				data.Documents = append(data.Documents, d)
			}
		}
	}
	return data
}

func regressionWorkerEngine(r *pipelineRuntime, rerank PipelineRerankOptions) *memoryservice.Engine {
	engine := memoryservice.NewEngine(r.memories, r.services.Vectors)
	engine.SetGenerationService(r.services.Generations)
	engine.SetSourceContextReader(r.conversations)
	configureExternalReranker(engine, rerank)
	return engine
}

func runRegressionGroup(ctx context.Context, t *testing.T, r *pipelineRuntime, g regressionGroup, plan regressionPlan, out, prefix string, embedding EmbeddingOptions, cache ports.EmbeddingClient, rerank PipelineRerankOptions, reader, judge LongMemEvalQAJudgeOptions, locomo Dataset, baselines map[string]regressionOutcome) {
	t.Helper()
	reader.ReasoningEffort = plan.ReaderThinking[g.Dataset]
	owner := prefix + "-" + pipelineHash(g.ID + "-workers")[:16]
	var workerError error
	workerCounts := map[string]int{}
	if g.Arena == nil {
		stages, err := r.ingest(ctx, owner, pipelineSample{ID: g.ID, Sessions: g.Sessions})
		workerError = err
		for _, stage := range stages {
			for _, d := range stage.Documents {
				workerCounts[string(d.Type)]++
			}
		}
	}
	for _, mode := range plan.Modes {
		if g.Arena != nil && mode == "none" {
			for _, q := range g.Questions {
				baseline, ok := baselines[q.ID]
				if !ok {
					t.Error("missing pinned Arena baseline")
					return
				}
				if err := writePipelineJSON(regressionOutcomePath(out, g, q, mode), baseline); err != nil {
					t.Error(err)
					return
				}
			}
			continue
		}
		if g.Arena != nil {
			workerCounts = map[string]int{}
		}
		modeOwner := owner
		var engine *memoryservice.Engine
		var bench *harness
		var setupErr error
		indexed := 0
		switch mode {
		case "raw":
			modeOwner = prefix + "-" + pipelineHash(g.ID + "-raw")[:16]
			dataset := regressionRawDataset(g, modeOwner, locomo)
			indexed = len(dataset.Documents)
			bench, setupErr = newHarnessWithClient(ctx, dataset, embedding, cache)
			if setupErr == nil {
				engine = bench.engine
				configureExternalReranker(engine, rerank)
			}
		case "derived", "combined":
			if g.Arena != nil {
				modeOwner = prefix + "-" + pipelineHash(g.ID + "-" + mode)[:16]
			} else {
				setupErr = workerError
			}
			engine = regressionWorkerEngine(r, rerank)
		}
		history := 0
		for _, q := range g.Questions {
			outcome := regressionOutcome{Dataset: g.Dataset, Group: g.ID, QuestionID: q.ID, Mode: mode, PriorStatus: q.Prior, WorkerDocuments: workerCounts, Answer: externalAnswer{Index: q.Index, Question: q.Query, Reference: q.Reference, Error: "attempt interrupted before completion"}}
			outcome.Recall = regressionMeasureSourceRecall(g, q, modeOwner, nil, nil)
			path := regressionOutcomePath(out, g, q, mode)
			if err := writePipelineJSON(path, outcome); err != nil {
				t.Error(err)
				return
			}
			if setupErr != nil {
				outcome.Answer.Error = "ingestion/index: " + setupErr.Error()
				_ = writePipelineJSON(path, outcome)
				continue
			}
			scoped, err := security.WithPersonalDataScope(ctx, modeOwner, "")
			if err != nil {
				t.Error(err)
				return
			}
			outcome.Answer.Error = ""
			if engine != nil && (g.Arena == nil || history > 0) {
				when := time.Date(2026, 10, 27, 0, 0, q.Index, 0, time.UTC)
				if g.Arena == nil {
					when = time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
					for _, session := range g.Sessions {
						if session.At.After(when) {
							when = session.At
						}
					}
					when = when.Add(24 * time.Hour)
					if g.LongMem != nil {
						when = longMemEvalTimeOrDefault(g.LongMem.QuestionDate, when)
					}
				}
				engine.SetClock(func() time.Time { return when })
				start := time.Now()
				input := domain.SearchInput{UserID: modeOwner, Query: q.Query, TopN: plan.TopN, IncludeSegments: mode == "raw" || mode == "combined", IncludeConversation: mode == "combined"}
				if mode == "raw" {
					if g.Locomo != nil {
						input.MemoryTypes = []domain.MemoryType{domain.MemoryTypeFact}
					} else {
						input.MemoryTypes = []domain.MemoryType{domain.MemoryTypeSegment}
					}
				}
				found, err := engine.SearchWithVariant(scoped, input, memoryservice.SearchVariantFull)
				outcome.Answer.SearchMillis = float64(time.Since(start).Microseconds()) / 1000
				outcome.Answer.Rerank = found.Rerank
				outcome.Timings = found.Timings
				outcome.Retrieval = &found
				if err == nil && bench != nil {
					err = bench.embeddingError()
				}
				if err == nil && (mode == "derived" || mode == "combined") {
					r.observed.mu.Lock()
					err = r.observed.err
					r.observed.mu.Unlock()
				}
				if err == nil && found.Rerank.Status != "applied" && (found.Rerank.Status != "skipped" || found.Rerank.Reason != "insufficient_candidates") {
					err = fmt.Errorf("reranker not applied: %s/%s", found.Rerank.Status, found.Rerank.Reason)
				}
				if err != nil {
					outcome.Answer.Error = "retrieval: " + err.Error()
					_ = writePipelineJSON(path, outcome)
					continue
				}
				var admitted []domain.MemoryDocument
				if mode == "combined" {
					start = time.Now()
					packet, err := engine.PrepareEvidence(scoped, input, found.Result.Memories, memoryservice.EvidenceOptions{MaxBytes: plan.ContextBytes, NeighborTurns: 1})
					outcome.EvidenceMillis = float64(time.Since(start).Microseconds()) / 1000
					outcome.Evidence = packet.Diagnostics
					if err != nil {
						outcome.Answer.Error = "evidence: " + err.Error()
						_ = writePipelineJSON(path, outcome)
						continue
					}
					outcome.Answer.Context = packet.Text
					admitted = packet.Memories
					for _, d := range packet.Memories {
						outcome.Answer.SourceIDs = append(outcome.Answer.SourceIDs, d.ID)
					}
				} else {
					outcome.Answer.Context, admitted, outcome.Answer.Omitted = regressionContext(found.Result.Memories, plan.ContextBytes)
					for _, doc := range admitted {
						outcome.Answer.SourceIDs = append(outcome.Answer.SourceIDs, doc.ID)
					}
				}
				outcome.Recall = regressionMeasureSourceRecall(g, q, modeOwner, found.Result.Memories, admitted)
			}
			outcome.Answer.Indexed = indexed
			outcome.Answer.ContextHash = pipelineHash(outcome.Answer.Context)
			payload := any(map[string]any{"question": q.Query, "question_date": q.Date, "memories": outcome.Answer.Context})
			instruction := pipelineInterpretationInstruction
			if g.Arena != nil {
				instruction = arenaReaderInstruction
				payload = map[string]any{"problem": arenaProblem(q.Query, g.Arena.Backgrounds[q.Index]), "memories": outcome.Answer.Context}
			}
			if g.Dataset == "locomo" {
				regressionLocomoRead(ctx, &outcome.Answer, reader, judge, payload, plan.ReaderTokens)
			} else {
				externalReadAndJudge(ctx, &outcome.Answer, reader, judge, instruction, payload, plan.ReaderTokens, q.LongMem)
			}
			// Own completed answers are source observations, regardless of their grade.
			if g.Arena != nil && mode != "none" && outcome.Answer.Answer != "" && !strings.HasPrefix(outcome.Answer.Error, "reader:") {
				if mode == "raw" {
					stored := outcome.Answer
					stored.Question = arenaProblem(q.Query, g.Arena.Backgrounds[q.Index])
					err = storeExternalExperience(scoped, bench, modeOwner, embedding.Model, q.Index, &stored)
				} else {
					at := time.Date(2026, 10, 27, 0, 0, q.Index, 0, time.UTC)
					session := pipelineSession{ID: fmt.Sprintf("experience-%03d", q.Index+1), Date: at.Format(time.RFC3339), At: at, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: arenaProblem(q.Query, g.Arena.Backgrounds[q.Index])}, {Role: domain.RoleAssistant, Text: outcome.Answer.Answer}}}
					stages, e := r.ingest(ctx, modeOwner, pipelineSample{ID: g.ID + "-" + mode, Sessions: []pipelineSession{session}})
					err = e
					for _, stage := range stages {
						for _, d := range stage.Documents {
							workerCounts[string(d.Type)]++
						}
					}
				}
				if err != nil {
					outcome.Answer.WriteError = err.Error()
					setupErr = err
				} else {
					indexed++
					history++
				}
			}
			if err = writePipelineJSON(path, outcome); err != nil {
				t.Error(err)
				return
			}
			t.Logf("regression %s %s correct=%t judged=%t error=%s write_error=%s", q.ID, mode, outcome.Answer.Correct, outcome.Answer.Judged, outcome.Answer.Error, outcome.Answer.WriteError)
		}
	}
}

func regressionLocomoRead(ctx context.Context, a *externalAnswer, reader, judge LongMemEvalQAJudgeOptions, payload any, budget int) {
	start := time.Now()
	answer, usage, model, err := pipelineReadObserved(ctx, reader, pipelineInterpretationInstruction, payload, budget)
	a.Answer, a.ReadUsage, a.ReaderModel = answer, usage, model
	a.ReadMillis = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		a.Error = "reader: " + err.Error()
		return
	}
	start = time.Now()
	raw, usage, model, err := pipelineCompleteObserved(ctx, judge, pipelineJudgeInstruction, map[string]any{"question": a.Question, "reference_answer": a.Reference, "candidate_answer": answer})
	a.RawJudge, a.JudgeUsage, a.JudgeModel = string(raw), usage, model
	a.JudgeMillis = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		a.Error = "judge: " + err.Error()
		return
	}
	var result struct {
		Correct *bool  `json:"correct"`
		Reason  string `json:"reason"`
	}
	if err = memoryllm.Decode(raw, &result); err != nil || result.Correct == nil || strings.TrimSpace(result.Reason) == "" {
		a.Error = "invalid judge schema"
		return
	}
	a.Correct, a.Judged = *result.Correct, true
}
