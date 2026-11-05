package eval

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	"memstation/apps/server/internal/service/analysis"
	memoryservice "memstation/apps/server/internal/service/memory"
	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"
)

// Export only stored original sources through the production window builder.
// It cannot finalize, ingest, invoke a model or read benchmark answer labels.
func TestExportFrozenProcessingSourceWindows(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_PROCESSING_SOURCE_PLAN")
	if path == "" {
		t.Skip("manual source-only encoder diagnosis")
	}
	var plan struct {
		Owner         string   `json:"owner"`
		Conversations []string `json:"conversations"`
		Output        string   `json:"output"`
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &plan) != nil || len(plan.Conversations) == 0 || len(plan.Conversations) > 2 || plan.Output == "" {
		t.Fatal("invalid source diagnosis plan", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, err = security.WithDataScope(ctx, plan.Owner, domain.PersonalWorkspaceID(plan.Owner))
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenEvaluationDatabase(ctx, os.Getenv("MEMSTATION_EVAL_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := conversationstore.NewPostgresConversationRepository(db)
	var batches []map[string]any
	for _, id := range plan.Conversations {
		conversation, err := repo.GetConversation(ctx, plan.Owner, id)
		if err != nil {
			t.Fatal(err)
		}
		_, texts, media := analysis.LightweightWindows(conversation)
		if media || len(texts) == 0 || len(texts) > 256 {
			t.Fatal("invalid source-only diagnosis")
		}
		batches = append(batches, map[string]any{"conversation_id": id, "texts": texts})
	}
	out, err := os.OpenFile(plan.Output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewEncoder(out).Encode(batches)
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
}

func TestRegressionGuardRejectsErrorsOmissionsAndIndividualLoss(t *testing.T) {
	b := PipelineAnswer{QuestionID: "question", SampleID: "conversation", Query: "A question", Expected: "reference", Mode: "combined", Category: 1, Judged: true, Correct: true, Context: "evidence", ContextHash: pipelineHash("evidence"), AnswerModel: "reader", JudgeModel: "judge"}
	if f := regressionFailures(b, b); len(f) != 0 {
		t.Fatal(f)
	}
	for name, mutate := range map[string]func(*PipelineAnswer){
		"lost":                            func(a *PipelineAnswer) { a.Correct = false },
		"unjudged":                        func(a *PipelineAnswer) { a.Judged = false },
		"execution_error_even_if_correct": func(a *PipelineAnswer) { a.Error = "failed" },
		"changed_question":                func(a *PipelineAnswer) { a.Query = "different" },
		"changed_reference":               func(a *PipelineAnswer) { a.Expected = "different" },
		"wrong_case":                      func(a *PipelineAnswer) { a.QuestionID = "another" },
		"changed_context":                 func(a *PipelineAnswer) { a.Context += " changed" },
		"changed_model":                   func(a *PipelineAnswer) { a.AnswerModel = "larger" },
		"omitted":                         func(a *PipelineAnswer) { *a = PipelineAnswer{} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := b
			mutate(&candidate)
			if len(regressionFailures(b, candidate)) == 0 {
				t.Fatal("regression accepted")
			}
		})
	}
}

// Replays production evidence preparation over frozen reranked candidates and
// the original read-only corpus. There is deliberately no embedding client,
// reranker, entity encoder, reader or judge. This proves input stability after
// fixed retrieval, not fresh answer accuracy or fresh retrieval equivalence.
func TestReplayLOCOMORegressionEvidence(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_REGRESSION_EVIDENCE_PLAN")
	if path == "" {
		t.Skip("manual frozen regression evidence replay; no inference")
	}
	var plan struct {
		Dataset       string   `json:"dataset"`
		DatasetHash   string   `json:"dataset_sha256"`
		Sample        string   `json:"sample"`
		Baseline      string   `json:"baseline"`
		BaselineHash  string   `json:"baseline_sha256"`
		Candidate     string   `json:"candidate"`
		CandidateHash string   `json:"candidate_sha256"`
		Cases         []string `json:"cases"`
		Output        string   `json:"output"`
	}
	load := func(path, hash string, out any) {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil || hash != "" && pipelineHash(string(raw)) != hash || json.Unmarshal(raw, out) != nil {
			t.Fatal("invalid frozen input", path, err)
		}
	}
	load(path, "", &plan)
	if len(plan.Cases) == 0 || len(plan.Cases) > 128 || plan.Output == "" || plan.BaselineHash == "" || plan.CandidateHash == "" {
		t.Fatal("bounded cohort and hashes required")
	}
	raw, err := os.ReadFile(plan.Dataset)
	if err != nil || pipelineHash(string(raw)) != plan.DatasetHash {
		t.Fatal("dataset identity", err)
	}
	samples, err := loadPipelineSamples(plan.Dataset, plan.Sample, 0)
	if err != nil || len(samples) != 1 {
		t.Fatal("single source sample required", err)
	}
	sample := samples[0]
	var baseline, candidate LOCOMOPipelineReport
	load(plan.Baseline, plan.BaselineHash, &baseline)
	load(plan.Candidate, plan.CandidateHash, &candidate)
	for _, key := range []string{"dataset_sha256", "answerer", "judge", "embedding", "processing", "rerank", "top_n", "reader_policy", "reader_format", "context_format", "context_byte_budget", "source_neighbor_turns", "qa_output_tokens", "judge_prompt_sha256"} {
		before, beforeOK := baseline.Manifest[key]
		after, afterOK := candidate.Manifest[key]
		if !beforeOK || !afterOK || before == nil || after == nil || !reflect.DeepEqual(before, after) {
			t.Fatal("protocol changed", key)
		}
	}
	if hash, ok := baseline.Manifest["answer_prompt_sha256"].(string); !ok || hash == "" || hash != candidate.Manifest["reader_prompt_sha256"] {
		t.Fatal("reader prompt changed")
	}
	index := func(rows []PipelineAnswer) map[string]PipelineAnswer {
		out := map[string]PipelineAnswer{}
		for _, row := range rows {
			if _, ok := out[row.QuestionID]; ok {
				t.Fatal("duplicate row")
			}
			out[row.QuestionID] = row
		}
		return out
	}
	before, after := index(baseline.Answers), index(candidate.Answers)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := OpenEvaluationDatabase(ctx, os.Getenv("MEMSTATION_EVAL_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var readonly, owner string
	var owners int
	if err = db.QueryRowContext(ctx, `SHOW default_transaction_read_only`).Scan(&readonly); err != nil || readonly != "on" {
		t.Fatal("read-only corpus required", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT min(user_id), count(DISTINCT user_id) FROM conversations`).Scan(&owner, &owners); err != nil || owners != 1 {
		t.Fatal("single owner required", err)
	}
	ctx, err = security.WithDataScope(ctx, owner, domain.PersonalWorkspaceID(owner))
	if err != nil {
		t.Fatal(err)
	}
	engine := memoryservice.NewEngine(memorystore.NewPostgresMemoryRepository(db), nil)
	engine.SetSourceContextReader(conversationstore.NewPostgresConversationRepository(db))
	budget := intValue(baseline.Manifest["context_byte_budget"])
	neighbors := intValue(baseline.Manifest["source_neighbor_turns"])
	seen := map[string]bool{}
	var results []map[string]any
	rejected, changed := 0, 0
	retrieval := func(row PipelineAnswer) memoryservice.DebugSearchResult {
		raw, err := json.Marshal(row.Retrieval)
		var found memoryservice.DebugSearchResult
		if err != nil || json.Unmarshal(raw, &found) != nil {
			t.Fatal("invalid retrieval snapshot", row.QuestionID, err)
		}
		return found
	}
	for _, id := range plan.Cases {
		if seen[id] {
			t.Fatal("duplicate cohort case")
		}
		seen[id] = true
		b, ok := before[id]
		if !ok || b.SampleID != sample.ID || !b.Judged || !b.Correct || b.Error != "" {
			t.Fatal("invalid protected baseline", id)
		}
		a := after[id] // Absence deliberately remains an execution failure.
		failures := regressionFailures(b, a)
		if len(failures) > 0 {
			rejected++
		}
		input := domain.SearchInput{UserID: owner, Query: b.Query}
		bFound, aFound := retrieval(b), retrieval(a)
		// The source-only engine receives just the question and authorized docs.
		packet, packErr := engine.PrepareEvidence(ctx, input, bFound.Candidates, memoryservice.EvidenceOptions{MaxBytes: budget})
		if packErr == nil {
			packet, packErr = engine.PrepareEvidence(ctx, input, packet.Memories, memoryservice.EvidenceOptions{MaxBytes: budget, NeighborTurns: neighbors})
		}
		stable := packErr == nil && pipelineHash(packet.Text) == b.ContextHash && reflect.DeepEqual(packet.SourceIDs, b.ContextSourceIDs)
		if !stable {
			changed++
		}
		// Gold annotations are consulted only AFTER production preparation.
		stage := func(row PipelineAnswer, found memoryservice.DebugSearchResult) map[string][]string {
			covered := func(refs []string) []string {
				var out []string
				for _, ref := range b.EvidenceRefs {
					if slices.Contains(refs, ref) {
						out = append(out, ref)
					}
				}
				return out
			}
			return map[string][]string{"reranked_pool": covered(pipelineSourceRefs(found.Candidates, owner, sample)), "result_page": covered(pipelineSourceRefs(found.Result.Memories, owner, sample)), "final_context": covered(row.SourceRefs)}
		}
		results = append(results, map[string]any{"case": id, "candidate_failures": failures, "restored_context_identical": stable, "baseline": stage(b, bFound), "candidate": stage(a, aFound), "baseline_context_sha256": b.ContextHash, "restored_context_sha256": pipelineHash(packet.Text)})
	}
	output, err := os.OpenFile(plan.Output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewEncoder(output).Encode(map[string]any{"scope": "offline archived-candidate rejection and restored evidence stability, not fresh answer accuracy", "cases": len(results), "candidate_rejections": rejected, "restored_context_changes": changed, "generation_calls": 0, "results": results})
	closeErr := output.Close()
	if err != nil || closeErr != nil || changed > 0 {
		t.Fatal("evidence regression", err, closeErr, changed)
	}
	if os.Getenv("MEMSTATION_ENFORCE_BENCHMARKS") == "true" && rejected > 0 {
		t.Fatalf("candidate rejected on %d protected cases", rejected)
	}
}
