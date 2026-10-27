package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/runtimeconfig"
	memoryservice "memstation/apps/server/internal/service/memory"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
	vectorstore "memstation/apps/server/internal/store/vector"
)

type evidenceReplayRow struct {
	Baseline PipelineAnswer `json:"baseline"`
	Group    string         `json:"group"`
}

type evidenceReplayPlan struct {
	SampleID       string              `json:"sample_id"`
	DatasetPath    string              `json:"dataset_path"`
	DatasetSHA256  string              `json:"dataset_sha256"`
	ParentSHA256   string              `json:"parent_sha256"`
	RecentSHA256   string              `json:"recent_sha256"`
	SnapshotSHA256 string              `json:"database_snapshot_sha256"`
	Manifest       map[string]any      `json:"manifest"`
	Rows           []evidenceReplayRow `json:"rows"`
}

// Manual diagnostic: use a read-only isolated database snapshot to hold extraction,
// vectors and relations constant, but execute current retrieval/reranking and fresh
// reader/judge calls. A failure-selected subset never estimates overall accuracy.
func TestLiveLOCOMOEvidenceReplay(t *testing.T) {
	planPath := os.Getenv("MEMSTATION_EVAL_EVIDENCE_REPLAY_PLAN")
	if planPath == "" {
		t.Skip("manual evidence retrieval replay")
	}
	out := os.Getenv("MEMSTATION_EVAL_EVIDENCE_REPLAY_OUT")
	revision := os.Getenv("MEMSTATION_EVAL_CODE_REVISION")
	if out == "" || revision == "" {
		t.Fatal("fresh output and current source fingerprint required")
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan evidenceReplayPlan
	if err = json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Rows) == 0 || plan.ParentSHA256 == "" || plan.DatasetSHA256 == "" {
		t.Fatal("missing replay provenance")
	}
	dataset, err := os.ReadFile(plan.DatasetPath)
	if err != nil || pipelineHash(string(dataset)) != plan.DatasetSHA256 {
		t.Fatal("dataset fingerprint mismatch", err)
	}
	if err = os.Mkdir(out, 0o700); err != nil {
		t.Fatal("repeats require a fresh output", err)
	}
	samples, err := loadPipelineSamples(plan.DatasetPath, plan.SampleID, 0)
	if err != nil || len(samples) != 1 {
		t.Fatal("exactly one sample required", err)
	}
	sample := samples[0]
	questions := map[string]pipelineQuestion{}
	for _, q := range sample.Questions {
		questions[q.ID] = q
	}
	seen := map[string]bool{}
	for _, row := range plan.Rows {
		a := row.Baseline
		q, ok := questions[a.QuestionID]
		key := a.QuestionID + "|" + a.Mode
		if !ok || seen[key] || q.Query != a.Query || q.Answer != a.Expected || q.Category != a.Category || a.SampleID != sample.ID || (a.Mode != "raw" && a.Mode != "derived" && a.Mode != "combined") || (row.Group != "previous_failure" && row.Group != "success_control" && row.Group != "evaluation") {
			t.Fatal("invalid, duplicate or mismatched replay selection", key)
		}
		seen[key] = true
	}
	cfg := func(role string) LongMemEvalQAJudgeOptions {
		return pipelineReplayRoleConfig(t, role)
	}
	stringField := func(k string) string { v, _ := plan.Manifest[k].(string); return v }
	intField := func(k string) int { v, _ := plan.Manifest[k].(float64); return int(v) }
	rerankData, _ := json.Marshal(plan.Manifest["rerank"])
	var pinned struct {
		Model      string `json:"model"`
		Revision   string `json:"model_revision"`
		Selection  string `json:"candidate_selection"`
		Multiplier int    `json:"candidate_multiplier"`
		Min        int    `json:"min_candidates"`
		Max        int    `json:"max_candidates"`
	}
	if err = json.Unmarshal(rerankData, &pinned); err != nil {
		t.Fatal(err)
	}
	options := LOCOMOPipelineOptions{DatasetPath: plan.DatasetPath, SampleID: sample.ID, OutputDir: out, TopN: intField("top_n"), ContextBytes: intField("context_byte_budget"), ReaderPolicy: stringField("reader_policy"), ContextFormat: stringField("context_format"), SourceNeighborTurns: intField("source_neighbor_turns"), Modes: []string{"raw", "derived", "combined"}, EmbeddingRevision: stringField("embedding_revision"), Embedding: EmbeddingOptions{Mode: EmbeddingModeProvider, Model: os.Getenv("EMBEDDING_MODEL"), BaseURL: os.Getenv("EMBEDDING_BASE_URL"), APIKey: os.Getenv("EMBEDDING_API_KEY")}, Answerer: cfg("ANSWERER"), Judge: cfg("JUDGE"), Rerank: PipelineRerankOptions{Model: pinned.Model, ModelRevision: pinned.Revision, BaseURL: os.Getenv("RERANK_BASE_URL"), APIKey: os.Getenv("RERANK_API_KEY"), CandidateSelection: pinned.Selection, CandidateMultiplier: pinned.Multiplier, MinCandidates: pinned.Min, MaxCandidates: pinned.Max}}
	options.RefineEvidence, _ = plan.Manifest["refine_evidence"].(bool)
	options.Planner = cfg("PLANNER")
	options.Modes = nil
	selectedModes := map[string]bool{}
	for _, row := range plan.Rows {
		if !selectedModes[row.Baseline.Mode] {
			options.Modes = append(options.Modes, row.Baseline.Mode)
			selectedModes[row.Baseline.Mode] = true
		}
	}
	options.Extraction = options.Answerer // validation only; extraction never runs here.
	if err = normalizePipelineProtocol(&options); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Minute)
	defer cancel()
	runtime := &pipelineRuntime{options: options}
	corpusOwner := ""
	sessions := 0
	if pipelineNeedsIngestion(options.Modes) {
		if plan.SnapshotSHA256 == "" {
			t.Fatal("database snapshot fingerprint required")
		}
		db, err := OpenEvaluationDatabase(ctx, os.Getenv("MEMSTATION_EVAL_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var readonly string
		if err = db.QueryRowContext(ctx, `SHOW default_transaction_read_only`).Scan(&readonly); err != nil || readonly != "on" {
			t.Fatal("snapshot connection must enforce read-only queries", err)
		}
		var count int
		if err = db.QueryRowContext(ctx, `SELECT min(user_id), count(DISTINCT user_id) FROM conversations`).Scan(&corpusOwner, &count); err != nil || count != 1 {
			t.Fatal("mixed or empty snapshot", err)
		}
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE user_id=$1 AND summary_status='completed' AND analysis_status='completed'`, corpusOwner).Scan(&sessions); err != nil || sessions != len(sample.Sessions) {
			t.Fatal("incomplete source corpus", err, sessions)
		}
		handle := runtimeconfig.NewStaticDBHandle(db)
		memories := memorystore.NewPostgresMemoryRepository(db)
		vectors := vectorstore.NewPGVectorStore(handle)
		embedder, err := newEvalEmbeddingClient(options.Embedding)
		if err != nil {
			t.Fatal(err)
		}
		observed := &observedEmbeddingClient{EmbeddingClient: embedder}
		vectors.SetEmbedder(observed)
		engine := memoryservice.NewEngine(memories, vectors)
		engine.SetGenerationService(memorygenservice.NewService(handle, options.Embedding.Model))
		engine.SetSourceContextReader(conversationstore.NewPostgresConversationRepository(db))
		configurePipelineReranker(engine, options.Rerank)
		runtime.db = db
		runtime.engine = engine
		runtime.memories = memories
		runtime.observed = observed
	}
	manifest := map[string]any{"experiment": "current_engine_frozen_corpus_evidence_replay", "code_revision": revision, "selection_plan_sha256": pipelineHash(string(data)), "parent_sha256": plan.ParentSHA256, "recent_sha256": plan.RecentSHA256, "database_snapshot_sha256": plan.SnapshotSHA256, "dataset_sha256": plan.DatasetSHA256, "reader_policy": options.ReaderPolicy, "answerer": options.Answerer.runInfo(), "judge": options.Judge.runInfo(), "embedding": options.Embedding.runInfo(), "rerank": options.Rerank.manifest(), "top_n": options.TopN, "context_byte_budget": options.ContextBytes, "context_format": options.ContextFormat, "source_neighbor_turns": options.SourceNeighborTurns, "reader_prompt_sha256": pipelineHash(pipelineReaderInstruction(options.ReaderPolicy)), "judge_prompt_sha256": pipelineHash(pipelineJudgeInstruction), "scope": "fixed extracted corpus when used; fresh retrieval/planner/selector/reader/judge; selected diagnostic, not overall accuracy", "read_only_database": pipelineNeedsIngestion(options.Modes)}
	manifest["refine_evidence"] = options.RefineEvidence
	manifest["coverage_policy"] = "additive_coverage_v2"
	manifest["selection_byte_budget"] = min(32000, max(1024, options.ContextBytes*2))
	manifest["planning_input"] = "question_only"
	manifest["planner"] = options.Planner.runInfo()
	manifest["planner_prompt_sha256"] = pipelineHash(memoryservice.EvidencePlanInstruction)
	manifest["selector_prompt_sha256"] = pipelineHash(memoryservice.EvidenceSelectionInstruction)
	manifest["persistence_identity_view"] = "opaque_before_packing_v1"
	manifest["context_admission"] = "scored_candidate_pool_byte_budget_v1"
	manifest["reader_format"] = "plain_text_v1"
	raw, _ := json.Marshal(manifest)
	options.manifestHash = pipelineHash(string(raw))
	runtime.options = options
	report := LOCOMOPipelineReport{Manifest: manifest, GeneratedAt: time.Now().UTC(), ExpectedSessions: sessions, CompletedSessions: sessions}
	for _, row := range plan.Rows {
		b := row.Baseline
		a := PipelineAnswer{SampleID: b.SampleID, QuestionID: b.QuestionID, Category: b.Category, Mode: b.Mode, Query: b.Query, Expected: b.Expected, EvidenceRefs: b.EvidenceRefs, Error: "not executed"}
		report.Answers = append(report.Answers, a)
	}
	save := func() {
		t.Helper()
		refreshPipelineSummaries(&report)
		if err = writePipelineJSON(filepath.Join(out, "pipeline.json"), report); err != nil {
			t.Fatal(err)
		}
	}
	save()
	for i, row := range plan.Rows {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		a := runtime.answer(ctx, corpusOwner, sample, questions[row.Baseline.QuestionID], row.Baseline.Mode)
		report.Answers[i] = a
		save()
		t.Logf("%d/%d %s %s group=%s correct=%t judged=%t error=%s rerank=%s", i+1, len(plan.Rows), a.QuestionID, a.Mode, row.Group, a.Correct, a.Judged, a.Error, a.Rerank.Status)
		if a.Error != "" {
			t.Errorf("%s %s execution failed: %s", a.QuestionID, a.Mode, a.Error)
		}
		if a.Error == "" && (a.Rerank.Status != "applied" || a.Rerank.ScorePolicy != "relevance_v1") {
			t.Errorf("%s %s did not apply current reranker", a.QuestionID, a.Mode)
		}
	}
}
