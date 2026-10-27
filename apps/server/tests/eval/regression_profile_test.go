package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/runtimeconfig"
	"memstation/apps/server/internal/security"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"
	vectorstore "memstation/apps/server/internal/store/vector"
	"memstation/apps/server/internal/worker"
)

type regressionProfilePlan struct {
	ParentHash string      `json:"parent_plan_sha256"`
	Questions  [][2]string `json:"questions"`
	Modes      []string    `json:"modes"`
	QAModes    []string    `json:"qa_modes"`
	Variants   []struct {
		Name       string `json:"name"`
		Model      string `json:"model"`
		Revision   string `json:"revision"`
		Candidates int    `json:"max_candidates"`
		TopN       int    `json:"top_n,omitempty"`
	} `json:"variants"`
}

type regressionProfileOutcome struct {
	Variant        string                           `json:"variant"`
	Mode           string                           `json:"mode"`
	Dataset        string                           `json:"dataset"`
	QuestionID     string                           `json:"question_id"`
	QA             bool                             `json:"qa"`
	Answer         externalAnswer                   `json:"answer"`
	Retrieval      *memoryservice.DebugSearchResult `json:"retrieval,omitempty"`
	Recall         *regressionSourceRecall          `json:"source_recall,omitempty"`
	EvidenceMillis float64                          `json:"evidence_millis"`
}

func TestLiveRegressionRetrievalProfile(t *testing.T) {
	profilePath := os.Getenv("MEMSTATION_EVAL_REGRESSION_PROFILE")
	if profilePath == "" {
		t.Skip("manual read-only static-cohort performance ablation")
	}
	raw, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	var profile regressionProfilePlan
	if err = json.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	rawPlan, err := os.ReadFile(os.Getenv("MEMSTATION_EVAL_REGRESSION_PLAN"))
	if err != nil {
		t.Fatal(err)
	}
	if pipelineHash(string(rawPlan)) != profile.ParentHash {
		t.Fatal("profile cohort differs from its preregistration")
	}
	var plan regressionPlan
	if err = json.Unmarshal(rawPlan, &plan); err != nil {
		t.Fatal(err)
	}
	groups, err := loadRegressionGroups(plan)
	if err != nil {
		t.Fatal(err)
	}
	selected := map[[2]string]bool{}
	for _, g := range groups {
		if g.Arena != nil {
			continue
		}
		dataset := g.Dataset
		if dataset == "longmemeval_s" {
			dataset = "longmemeval"
		}
		for _, q := range g.Questions {
			selected[[2]string{dataset, q.ID}] = true
		}
	}
	if len(profile.Questions) != len(selected) {
		t.Fatal("profile changed the static question denominator")
	}
	for _, question := range profile.Questions {
		if !selected[question] {
			t.Fatal("profile omitted, repeated or changed a static question")
		}
		delete(selected, question)
	}
	parent := os.Getenv("MEMSTATION_EVAL_REGRESSION_PARENT")
	out := os.Getenv("MEMSTATION_EVAL_REGRESSION_PROFILE_OUT")
	var original struct {
		Revision string `json:"code_revision"`
		Hash     string `json:"plan_sha256"`
	}
	rawParent, err := os.ReadFile(filepath.Join(parent, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(rawParent, &original); err != nil || original.Hash != profile.ParentHash {
		t.Fatal("parent manifest mismatch", err)
	}
	var completion struct {
		Cancelled bool `json:"cancelled"`
	}
	rawCompletion, err := os.ReadFile(filepath.Join(parent, "completion.json"))
	if err != nil {
		t.Fatal("primary must complete first", err)
	}
	if err = json.Unmarshal(rawCompletion, &completion); err != nil || completion.Cancelled {
		t.Fatal("primary incomplete", err)
	}
	if len(profile.Variants) == 0 || strings.Join(profile.Modes, ",") != "raw,derived,combined" || strings.Join(profile.QAModes, ",") != "derived,combined" {
		t.Fatal("invalid preregistered variants")
	}
	variants := map[string]bool{}
	for _, variant := range profile.Variants {
		if variants[variant.Name] || strings.TrimSpace(variant.Name) == "" || len(variant.Name) > 80 {
			t.Fatal("invalid or repeated variant")
		}
		if _, _, err := domain.ParseRerankModel(variant.Model); err != nil {
			t.Fatal(err)
		}
		variants[variant.Name] = true
	}
	if err = os.Mkdir(out, 0o700); err != nil {
		t.Fatal("fresh private output required", err)
	}
	ctx := context.Background()
	db, err := OpenEvaluationDatabase(ctx, os.Getenv("MEMSTATION_EVAL_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Fail before queries if a writer credential was accidentally supplied.
	var writer bool
	if err = db.QueryRowContext(ctx, `SELECT r.rolsuper OR has_table_privilege(current_user,'memory_nodes','INSERT') OR has_table_privilege(current_user,'memory_nodes','UPDATE') OR has_table_privilege(current_user,'memory_nodes','DELETE') FROM pg_roles r WHERE r.rolname=current_user`).Scan(&writer); err != nil || writer {
		t.Fatal("SELECT-only evaluation credential required", err)
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
		t.Fatal("pinned content cache required")
	}
	observed := &observedEmbeddingClient{EmbeddingClient: cache}
	vectors := vectorstore.NewPGVectorStore(runtimeconfig.NewStaticDBHandle(db))
	vectors.SetEmbedder(observed)
	base := &pipelineRuntime{memories: memorystore.NewPostgresMemoryRepository(db), conversations: conversationstore.NewPostgresConversationRepository(db), services: worker.Services{Vectors: vectors, Generations: memorygenservice.NewService(runtimeconfig.NewStaticDBHandle(db), embedding.Model)}}
	locomo, err := LoadLOCOMODataset(plan.Locomo.Path, LOCOMOOptions{SkipCategoryFive: true})
	if err != nil {
		t.Fatal(err)
	}
	reader := regressionRole(t, "ANSWERER", plan.MaxTokens, "")
	judge := regressionRole(t, "JUDGE", plan.MaxTokens, "")
	prefix := "eval-regression-" + pipelineHash(string(rawPlan) + original.Revision)[:12]
	staticQuestions := 0
	for _, g := range groups {
		if g.Arena == nil {
			staticQuestions += len(g.Questions)
		}
	}
	manifest := map[string]any{"profile_plan_sha256": pipelineHash(string(raw)), "parent_manifest_sha256": pipelineHash(string(rawParent)), "code_revision": os.Getenv("MEMSTATION_EVAL_CODE_REVISION"), "expected_searches": staticQuestions * len(profile.Modes) * len(profile.Variants), "expected_qa": staticQuestions * len(profile.QAModes) * len(profile.Variants), "profile_plan": profile, "scope": "every static cohort case; Arena excluded to avoid future histories; fixed parent corpus and SELECT-only database; warm content cache, sequential model requests, new QA baseline controls"}
	if err = writePipelineJSON(filepath.Join(out, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	pathFor := func(g regressionGroup, q regressionQuestion, variant, mode string) string {
		return filepath.Join(out, pipelineHash(g.ID+"\x00"+q.ID+"\x00"+variant+"\x00"+mode)+".json")
	}
	for _, g := range groups {
		if g.Arena != nil {
			continue
		}
		for _, q := range g.Questions {
			for _, v := range profile.Variants {
				for _, mode := range profile.Modes {
					row := regressionProfileOutcome{Variant: v.Name, Mode: mode, Dataset: g.Dataset, QuestionID: q.ID, QA: mode != "raw", Answer: externalAnswer{Question: q.Query, Reference: q.Reference, Error: "not executed"}}
					if err = writePipelineJSON(pathFor(g, q, v.Name, mode), row); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	for _, g := range groups {
		if g.Arena != nil {
			continue
		}
		reader.ReasoningEffort = plan.ReaderThinking[g.Dataset]
		rawOwner := prefix + "-" + pipelineHash(g.ID + "-raw")[:16]
		workerOwner := prefix + "-" + pipelineHash(g.ID + "-workers")[:16]
		bench, setupErr := newHarnessWithClient(ctx, regressionRawDataset(g, rawOwner, locomo), embedding, cache)
		for _, q := range g.Questions {
			for _, v := range profile.Variants {
				for _, mode := range profile.Modes {
					row := regressionProfileOutcome{Variant: v.Name, Mode: mode, Dataset: g.Dataset, QuestionID: q.ID, QA: mode != "raw", Answer: externalAnswer{Question: q.Query, Reference: q.Reference, Error: "attempt interrupted before completion"}}
					path := pathFor(g, q, v.Name, mode)
					if err = writePipelineJSON(path, row); err != nil {
						t.Fatal(err)
					}
					row.Answer.Error = ""
					if mode != "raw" {
						raw, readErr := os.ReadFile(regressionOutcomePath(parent, g, q, mode))
						var prior regressionOutcome
						if readErr != nil || json.Unmarshal(raw, &prior) != nil || prior.QuestionID != q.ID || prior.Mode != mode {
							t.Fatal("missing or mismatched parent outcome", readErr)
						}
						// A partial parent corpus must not silently become a valid
						// profile arm. Keep the error in every variant's denominator.
						if strings.HasPrefix(prior.Answer.Error, "ingestion/index:") {
							row.Answer.Error = "parent " + prior.Answer.Error
							if err = writePipelineJSON(path, row); err != nil {
								t.Fatal(err)
							}
							continue
						}
					}
					cfg := PipelineRerankOptions{Model: v.Model, ModelRevision: v.Revision, APIKey: os.Getenv("RERANK_API_KEY"), BaseURL: os.Getenv("RERANK_BASE_URL"), Timeout: 3 * time.Minute, CandidateSelection: "balanced", CandidateMultiplier: 6, MinCandidates: 20, MaxCandidates: v.Candidates}
					if strings.HasPrefix(v.Name, "0.6b-") {
						cfg.BaseURL = os.Getenv("MEMSTATION_EVAL_SMALL_RERANK_BASE_URL")
					}
					topN := plan.TopN
					if v.TopN > 0 {
						topN = v.TopN
					}
					if cfg.BaseURL == "" || cfg.ModelRevision == "" || v.Candidates < topN || v.Candidates > 200 || topN < 1 || topN > 100 {
						t.Fatal("invalid pinned reranker profile")
					}
					owner := workerOwner
					engine := regressionWorkerEngine(base, cfg)
					if mode == "raw" {
						owner = rawOwner
						if setupErr != nil {
							row.Answer.Error = "index: " + setupErr.Error()
							_ = writePipelineJSON(path, row)
							continue
						}
						engine = bench.engine
						configureExternalReranker(engine, cfg)
					}
					when := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
					for _, s := range g.Sessions {
						if s.At.After(when) {
							when = s.At
						}
					}
					when = when.Add(24 * time.Hour)
					if g.LongMem != nil {
						when = longMemEvalTimeOrDefault(g.LongMem.QuestionDate, when)
					}
					engine.SetClock(func() time.Time { return when })
					scoped, e := security.WithPersonalDataScope(ctx, owner, "")
					if e != nil {
						t.Fatal(e)
					}
					input := domain.SearchInput{UserID: owner, Query: q.Query, TopN: topN, IncludeSegments: mode != "derived", IncludeConversation: mode == "combined"}
					if mode == "raw" {
						input.MemoryTypes = []domain.MemoryType{domain.MemoryTypeSegment}
						if g.Locomo != nil {
							input.MemoryTypes = []domain.MemoryType{domain.MemoryTypeFact}
						}
					}
					start := time.Now()
					found, e := engine.SearchWithVariant(scoped, input, memoryservice.SearchVariantFull)
					row.Answer.SearchMillis = float64(time.Since(start).Microseconds()) / 1000
					row.Answer.Rerank = found.Rerank
					row.Retrieval = &found
					if e == nil && mode == "raw" {
						e = bench.embeddingError()
					}
					if e == nil && mode != "raw" {
						observed.mu.Lock()
						e = observed.err
						observed.mu.Unlock()
					}
					if e == nil && found.Rerank.Status != "applied" && (found.Rerank.Status != "skipped" || found.Rerank.Reason != "insufficient_candidates") {
						e = fmt.Errorf("reranker not applied: %s/%s", found.Rerank.Status, found.Rerank.Reason)
					}
					if e != nil {
						row.Answer.Error = "retrieval: " + e.Error()
						_ = writePipelineJSON(path, row)
						continue
					}
					var admitted []domain.MemoryDocument
					if mode == "combined" {
						start = time.Now()
						packet, e := engine.PrepareEvidence(scoped, input, found.Result.Memories, memoryservice.EvidenceOptions{MaxBytes: plan.ContextBytes, NeighborTurns: 1})
						row.EvidenceMillis = float64(time.Since(start).Microseconds()) / 1000
						if e != nil {
							row.Answer.Error = "evidence: " + e.Error()
							_ = writePipelineJSON(path, row)
							continue
						}
						row.Answer.Context = packet.Text
						admitted = packet.Memories
						for _, doc := range packet.Memories {
							row.Answer.SourceIDs = append(row.Answer.SourceIDs, doc.ID)
						}
					} else {
						row.Answer.Context, admitted, row.Answer.Omitted = regressionContext(found.Result.Memories, plan.ContextBytes)
						for _, doc := range admitted {
							row.Answer.SourceIDs = append(row.Answer.SourceIDs, doc.ID)
						}
					}
					row.Recall = regressionMeasureSourceRecall(g, q, owner, found.Result.Memories, admitted)
					row.Answer.ContextHash = pipelineHash(row.Answer.Context)
					if row.QA {
						payload := map[string]any{"question": q.Query, "question_date": q.Date, "memories": row.Answer.Context}
						if g.Dataset == "locomo" {
							regressionLocomoRead(ctx, &row.Answer, reader, judge, payload, plan.ReaderTokens)
						} else {
							externalReadAndJudge(ctx, &row.Answer, reader, judge, pipelineInterpretationInstruction, payload, plan.ReaderTokens, q.LongMem)
						}
					}
					if e = writePipelineJSON(path, row); e != nil {
						t.Fatal(e)
					}
					t.Logf("profile %s %s %s latency_ms=%.1f judged=%t correct=%t error=%s", v.Name, mode, q.ID, row.Answer.SearchMillis, row.Answer.Judged, row.Answer.Correct, row.Answer.Error)
				}
			}
		}
	}
	if err = writePipelineJSON(filepath.Join(out, "completion.json"), map[string]any{"completed_at": time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}
