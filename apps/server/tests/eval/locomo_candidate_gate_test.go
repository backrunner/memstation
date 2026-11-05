package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"memstation/apps/server/internal/domain"
)

type candidateGateFixture struct {
	plan      LOCOMOCandidatePlan
	candidate LOCOMOPipelineReport
	path      string
}

func writeCandidateFixture(t *testing.T, path string, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return pipelineHash(string(raw))
}

func sealCandidateFixture(report *LOCOMOPipelineReport) {
	raw, _ := json.Marshal(report.Manifest)
	for i := range report.Answers {
		report.Answers[i].ManifestHash = pipelineHash(string(raw))
	}
}

const candidateGateFixtureCases = 240

func newCandidateGateFixture(t *testing.T) candidateGateFixture {
	t.Helper()
	root := t.TempDir()
	manifest := map[string]any{
		"version": float64(4), "code_revision": "baseline", "dataset_sha256": pipelineHash("source dataset"),
		"answerer": map[string]any{"model": "reader"}, "judge": map[string]any{"model": "judge"},
		"embedding": map[string]any{"model": "embedding"}, "embedding_revision": "frozen-embedding",
		"processing": map[string]any{"strategy": "lightweight"}, "rerank": map[string]any{"max_candidates": 50},
		"top_n": 20, "reader_policy": "interpretation", "reader_format": "plain_text_v1", "context_format": "evidence",
		"context_byte_budget": 16000, "source_neighbor_turns": 1, "qa_output_tokens": 16384,
		"judge_initial_output_tokens": 4096, "judge_prompt_sha256": pipelineHash("judge"),
		"answer_prompt_sha256": pipelineHash("reader"), "refine_evidence": false,
		"image_policy": "raw text only", "judge_protocol": "reference agreement",
	}
	// Match real runInfo values: typed field order differs from map key order.
	manifest["answerer"] = struct {
		Model    string `json:"model"`
		Endpoint string `json:"endpoint"`
	}{"reader", "local"}
	b := LOCOMOPipelineReport{Manifest: manifest, ExpectedSessions: 2, CompletedSessions: 2}
	for i := 0; i < candidateGateFixtureCases; i++ {
		id := fmt.Sprintf("q-%03d", i)
		b.Answers = append(b.Answers, PipelineAnswer{SampleID: "sample", QuestionID: id, Mode: "combined", Category: i%4 + 1, Query: "Synthetic question " + id, Expected: "reference", Answer: "answer", Judged: true, Correct: i%2 == 0, Context: "source evidence", ContextHash: pipelineHash("source evidence"), ContextBytes: len("source evidence"), AnswerModel: "reader", JudgeModel: "judge", AnswerUsage: domain.UsageRecord{InputTokens: 100, OutputTokens: 10, TotalTokens: 110}, JudgeUsage: domain.UsageRecord{InputTokens: 20, OutputTokens: 5, TotalTokens: 25}})
	}
	for i := range b.Answers {
		b.Answers[i].EntityRecall = &domain.EntityRecallDiagnostics{Status: "complete"}
	}
	sealCandidateFixture(&b)
	baselinePath := filepath.Join(root, "baseline.json")
	plan := LOCOMOCandidatePlan{Version: 2, Purpose: "replacement", CandidateCodeRevision: "candidate", MaxAnswerInputTokenRatio: 1.05, MaxQATotalTokens: 40000, Baselines: []LOCOMOArtifact{{baselinePath, writeCandidateFixture(t, baselinePath, b)}}}
	for _, a := range b.Answers {
		plan.Cases = append(plan.Cases, candidateKey(a))
	}
	// Repeat of the same code: one question flips each way, as reader noise does.
	repeat := b
	repeat.Answers = slices.Clone(b.Answers)
	repeat.Answers[0].Correct, repeat.Answers[1].Correct = false, true
	repeatPath := filepath.Join(root, "repeat.json")
	plan.RepeatBaselines = []LOCOMOArtifact{{repeatPath, writeCandidateFixture(t, repeatPath, repeat)}}
	b.Manifest["code_revision"] = "candidate"
	for i := range b.Answers {
		b.Answers[i].Correct = true
	}
	sealCandidateFixture(&b)
	return candidateGateFixture{plan: plan, candidate: b, path: filepath.Join(root, "candidate.json")}
}

func TestCandidateGateToleratesNoiseLevelLossWithSignificantGain(t *testing.T) {
	f := newCandidateGateFixture(t)
	f.candidate.Answers[0].Correct = false
	writeCandidateFixture(t, f.path, f.candidate)
	v, err := CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || !v.Passed || !v.ReplacementEligible || v.Regressions != 1 || v.Gains != 120 || v.NoiseFlips != 2 {
		t.Fatal(v, err)
	}
}

func TestCandidateGateRejectsLossBeyondNoiseDespiteAggregateGain(t *testing.T) {
	f := newCandidateGateFixture(t)
	for i := 0; i < 40; i += 2 {
		f.candidate.Answers[i].Correct = false
	}
	writeCandidateFixture(t, f.path, f.candidate)
	v, err := CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || v.Passed || v.ReplacementEligible || !slices.Contains(v.Failures, "regressions_exceed_noise") {
		t.Fatal(v, err)
	}
}

func TestCandidateGateRequiresGainBeyondNoise(t *testing.T) {
	f := newCandidateGateFixture(t)
	for i := range f.candidate.Answers {
		f.candidate.Answers[i].Correct = i%2 == 0 || i < 12
	}
	writeCandidateFixture(t, f.path, f.candidate)
	v, err := CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || !v.Passed || v.Gains != 6 {
		t.Fatal(v, err)
	}
	for i := range f.candidate.Answers {
		f.candidate.Answers[i].Correct = i%2 == 0 || i < 4
	}
	writeCandidateFixture(t, f.path, f.candidate)
	v, err = CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || v.Passed || !slices.Contains(v.Failures, "gain_not_significant") {
		t.Fatal(v, err)
	}
	f.plan.Purpose = "screen"
	v, err = CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || !v.Passed || v.ReplacementEligible {
		t.Fatal(v, err)
	}
}

func TestExactMcNemarP(t *testing.T) {
	if p := exactMcNemarP(0, 0); p != 1 {
		t.Fatal(p)
	}
	if p := exactMcNemarP(1, 0); p != 1 {
		t.Fatal(p)
	}
	if p := exactMcNemarP(6, 0); math.Abs(p-.03125) > 1e-9 {
		t.Fatal(p)
	}
	if p := exactMcNemarP(66, 83); p < .1 || p > .25 {
		t.Fatal(p)
	}
}

func TestCandidateGateFailsClosed(t *testing.T) {
	for name, mutate := range map[string]func(*candidateGateFixture){
		"missing_row":           func(f *candidateGateFixture) { f.candidate.Answers = f.candidate.Answers[1:] },
		"missing_report":        func(f *candidateGateFixture) { f.path += "/absent" },
		"unjudged":              func(f *candidateGateFixture) { f.candidate.Answers[0].Judged = false },
		"missing_entity_recall": func(f *candidateGateFixture) { f.candidate.Answers[0].EntityRecall = nil },
		"failed_entity_recall": func(f *candidateGateFixture) {
			f.candidate.Answers[0].EntityRecall = &domain.EntityRecallDiagnostics{Status: "unavailable"}
		},
		"error_marked_correct": func(f *candidateGateFixture) { f.candidate.Answers[0].Error = "timeout" },
		"duplicate": func(f *candidateGateFixture) {
			f.candidate.Answers = append(f.candidate.Answers, f.candidate.Answers[0])
		},
		"question_changed":   func(f *candidateGateFixture) { f.candidate.Answers[0].Query = "different" },
		"reference_changed":  func(f *candidateGateFixture) { f.candidate.Answers[0].Expected = "different" },
		"category_changed":   func(f *candidateGateFixture) { f.candidate.Answers[0].Category = 2 },
		"returned_model":     func(f *candidateGateFixture) { f.candidate.Answers[0].JudgeModel = "different" },
		"bad_context_hash":   func(f *candidateGateFixture) { f.candidate.Answers[0].Context += " changed" },
		"bad_manifest_hash":  func(f *candidateGateFixture) { f.candidate.Answers[0].ManifestHash = "wrong" },
		"wrong_revision":     func(f *candidateGateFixture) { f.plan.CandidateCodeRevision = "unmeasured" },
		"missing_usage":      func(f *candidateGateFixture) { f.candidate.Answers[0].AnswerUsage = domain.UsageRecord{} },
		"inconsistent_usage": func(f *candidateGateFixture) { f.candidate.Answers[0].AnswerUsage.TotalTokens++ },
		"input_budget":       func(f *candidateGateFixture) { f.plan.MaxAnswerInputTokenRatio = .9 },
		"total_budget":       func(f *candidateGateFixture) { f.plan.MaxQATotalTokens = 30000 },
		"oracle":             func(f *candidateGateFixture) { f.candidate.Answers[0].DiagnosticOnly = true },
		"extra_generation":   func(f *candidateGateFixture) { f.candidate.Answers[0].PlannerUsage.TotalTokens = 1 },
		"partial_ingestion":  func(f *candidateGateFixture) { f.candidate.CompletedSessions-- },
		"fewer_source_sessions": func(f *candidateGateFixture) {
			f.candidate.ExpectedSessions--
			f.candidate.CompletedSessions--
		},
		"stage_error": func(f *candidateGateFixture) { f.candidate.Stages = []PipelineStage{{Error: "failed"}} },
		"reader_prompt": func(f *candidateGateFixture) {
			f.candidate.Manifest["answer_prompt_sha256"] = pipelineHash("different")
			sealCandidateFixture(&f.candidate)
		},
		"missing_protocol": func(f *candidateGateFixture) {
			delete(f.candidate.Manifest, "image_policy")
			sealCandidateFixture(&f.candidate)
		},
		"larger_context_budget": func(f *candidateGateFixture) {
			f.candidate.Manifest["context_byte_budget"] = 32000
			sealCandidateFixture(&f.candidate)
		},
		"wrong_corpus": func(f *candidateGateFixture) {
			delete(f.candidate.Manifest, "version")
			f.candidate.Manifest["experiment"] = "current_engine_frozen_corpus_evidence_replay"
			f.candidate.Manifest["reader_prompt_sha256"] = f.candidate.Manifest["answer_prompt_sha256"]
			f.candidate.Manifest["parent_sha256"] = pipelineHash("unrelated corpus")
			f.candidate.Manifest["database_snapshot_sha256"] = pipelineHash("snapshot")
			f.candidate.Manifest["read_only_database"] = true
			sealCandidateFixture(&f.candidate)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCandidateGateFixture(t)
			mutate(&f)
			if name != "missing_report" {
				writeCandidateFixture(t, f.path, f.candidate)
			}
			v, err := CheckLOCOMOCandidate(f.plan, []string{f.path})
			if err != nil || v.Passed || v.ReplacementEligible || len(v.Cases) != candidateGateFixtureCases || v.Expected != candidateGateFixtureCases {
				t.Fatal("invalid candidate escaped fixed denominator", v, err)
			}
		})
	}
}

func TestCandidateGateRejectsInvalidPlanAndBaseline(t *testing.T) {
	for name, mutate := range map[string]func(*candidateGateFixture){
		"empty_cohort":          func(f *candidateGateFixture) { f.plan.Cases = nil },
		"duplicate_case":        func(f *candidateGateFixture) { f.plan.Cases = append(f.plan.Cases, f.plan.Cases[0]) },
		"baseline_tampered":     func(f *candidateGateFixture) { f.plan.Baselines[0].SHA256 = pipelineHash("different") },
		"missing_baseline_case": func(f *candidateGateFixture) { f.plan.Cases[0].QuestionID = "absent" },
		"nan_budget":            func(f *candidateGateFixture) { f.plan.MaxAnswerInputTokenRatio = math.NaN() },
		"infinite_budget":       func(f *candidateGateFixture) { f.plan.MaxAnswerInputTokenRatio = math.Inf(1) },
		"no_budget":             func(f *candidateGateFixture) { f.plan.MaxQATotalTokens = 0 },
		"no_repeat_baseline":    func(f *candidateGateFixture) { f.plan.RepeatBaselines = nil },
		"repeat_tampered":       func(f *candidateGateFixture) { f.plan.RepeatBaselines[0].SHA256 = pipelineHash("different") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newCandidateGateFixture(t)
			mutate(&f)
			writeCandidateFixture(t, f.path, f.candidate)
			if _, err := CheckLOCOMOCandidate(f.plan, []string{f.path}); err == nil {
				t.Fatal("invalid plan or baseline accepted")
			}
		})
	}
}

func TestCandidateGateRequiresPowerAndGainForReplacement(t *testing.T) {
	f := newCandidateGateFixture(t)
	f.plan.Cases = f.plan.Cases[:40]
	writeCandidateFixture(t, f.path, f.candidate)
	v, err := CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || v.Passed || !slices.Contains(v.Failures, "too_few_cases_for_replacement") {
		t.Fatal("underpowered cohort authorized replacement", v, err)
	}
	f.plan.Purpose = "screen"
	v, err = CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || !v.Passed || v.ReplacementEligible {
		t.Fatal(v, err)
	}
}

func TestCandidateGateReceiptCannotBeReusedOrWrittenOnFailure(t *testing.T) {
	f := newCandidateGateFixture(t)
	root := filepath.Dir(f.path)
	planPath, reportsPath := filepath.Join(root, "plan.json"), filepath.Join(root, "reports.json")
	hash := writeCandidateFixture(t, planPath, f.plan)
	writeCandidateFixture(t, reportsPath, []string{f.path})
	writeCandidateFixture(t, f.path, f.candidate)
	out := filepath.Join(root, "accepted")
	v, err := RunLOCOMOCandidateGate(planPath, hash, reportsPath, out)
	if err != nil || !v.ReplacementEligible {
		t.Fatal(v, err)
	}
	var receipt struct {
		VerdictSHA256 string `json:"verdict_sha256"`
		PlanSHA256    string `json:"plan_sha256"`
	}
	raw, err := os.ReadFile(filepath.Join(out, "acceptance.json"))
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.PlanSHA256 != hash {
		t.Fatal("missing bound receipt", err)
	}
	verdict, _ := os.ReadFile(filepath.Join(out, "verdict.json"))
	if receipt.VerdictSHA256 != pipelineHash(string(verdict)) {
		t.Fatal("receipt did not bind exact verdict")
	}
	if _, err = RunLOCOMOCandidateGate(planPath, hash, reportsPath, out); err == nil {
		t.Fatal("overwrote prior verdict")
	}
	for i := range f.candidate.Answers {
		f.candidate.Answers[i].Correct = i%2 == 0
	}
	writeCandidateFixture(t, f.path, f.candidate)
	out = filepath.Join(root, "rejected")
	if _, err = RunLOCOMOCandidateGate(planPath, hash, reportsPath, out); err == nil {
		t.Fatal("loss accepted")
	}
	if _, err = os.Stat(filepath.Join(out, "acceptance.json")); !os.IsNotExist(err) {
		t.Fatal("failed attempt left success receipt")
	}
	if _, err = os.Stat(filepath.Join(out, "verdict.json")); err != nil {
		t.Fatal("rejected attempt lost its diagnostic", err)
	}
	f.plan.Cases = f.plan.Cases[1:]
	writeCandidateFixture(t, planPath, f.plan)
	if _, err = RunLOCOMOCandidateGate(planPath, hash, reportsPath, filepath.Join(root, "tampered")); err == nil {
		t.Fatal("post-result cohort shrink accepted")
	}
}

func TestCandidateGateRefinementMustBeDeclaredAndBudgeted(t *testing.T) {
	f := newCandidateGateFixture(t)
	f.candidate.Manifest["refine_evidence"] = true
	for i := range f.candidate.Answers {
		f.candidate.Answers[i].PlannerUsage = domain.UsageRecord{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
		f.candidate.Answers[i].PlannerModel = "reader"
	}
	sealCandidateFixture(&f.candidate)
	writeCandidateFixture(t, f.path, f.candidate)
	v, err := CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || v.Passed {
		t.Fatal("undeclared refinement accepted", v, err)
	}
	f.plan.DeclaredRefinement = true
	v, err = CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || !v.Passed || v.CandidateQATotalTokens != 240*(110+25+15) {
		t.Fatal(v, err)
	}
	f.plan.MaxQATotalTokens = 33000
	v, err = CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || v.Passed || !slices.Contains(v.Failures, "qa_token_budget_exceeded") {
		t.Fatal("planner tokens escaped the budget", v, err)
	}
}

func TestCandidateGateIgnoresEndpointButNotModel(t *testing.T) {
	f := newCandidateGateFixture(t)
	f.candidate.Manifest["judge"] = map[string]any{"model": "judge", "base_url": "http://127.0.0.1:1"}
	sealCandidateFixture(&f.candidate)
	writeCandidateFixture(t, f.path, f.candidate)
	v, err := CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || !v.Passed {
		t.Fatal("endpoint route treated as a protocol change", v, err)
	}
	f.candidate.Manifest["judge"] = map[string]any{"model": "other", "base_url": "http://127.0.0.1:1"}
	sealCandidateFixture(&f.candidate)
	writeCandidateFixture(t, f.path, f.candidate)
	v, err = CheckLOCOMOCandidate(f.plan, []string{f.path})
	if err != nil || v.Passed {
		t.Fatal("judge model change accepted", v, err)
	}
}

func TestCandidateGateLargerContextMustBeDeclared(t *testing.T) {
	f := newCandidateGateFixture(t)
	f.candidate.Manifest["context_byte_budget"] = 32000
	sealCandidateFixture(&f.candidate)
	writeCandidateFixture(t, f.path, f.candidate)
	if v, err := CheckLOCOMOCandidate(f.plan, []string{f.path}); err != nil || v.Passed {
		t.Fatal("undeclared budget growth accepted", v, err)
	}
	f.plan.DeclaredContextBytes = 24000
	if v, err := CheckLOCOMOCandidate(f.plan, []string{f.path}); err != nil || v.Passed {
		t.Fatal("budget other than the declared one accepted", v, err)
	}
	f.plan.DeclaredContextBytes = 32000
	if v, err := CheckLOCOMOCandidate(f.plan, []string{f.path}); err != nil || !v.Passed {
		t.Fatal(v, err)
	}
}

func TestCandidateGateCostReductionNeedsSavingsWithoutLoss(t *testing.T) {
	f := newCandidateGateFixture(t)
	f.plan.Purpose = "cost_reduction"
	f.plan.DeclaredContextBytes = 12000
	cheaper := func(correct func(int) bool, input int) {
		f.candidate.Manifest["context_byte_budget"] = 12000
		for i := range f.candidate.Answers {
			f.candidate.Answers[i].Correct = correct(i)
			f.candidate.Answers[i].AnswerUsage = domain.UsageRecord{InputTokens: input, OutputTokens: 10, TotalTokens: input + 10}
		}
		sealCandidateFixture(&f.candidate)
		writeCandidateFixture(t, f.path, f.candidate)
	}
	cheaper(func(i int) bool { return i%2 == 0 }, 70)
	if v, err := CheckLOCOMOCandidate(f.plan, []string{f.path}); err != nil || !v.Passed || !v.ReplacementEligible {
		t.Fatal("equal answers for less input rejected", v, err)
	}
	cheaper(func(i int) bool { return i%2 == 0 }, 100)
	if v, err := CheckLOCOMOCandidate(f.plan, []string{f.path}); err != nil || v.Passed || !slices.Contains(v.Failures, "no_token_reduction") {
		t.Fatal("no saving accepted", v, err)
	}
	cheaper(func(i int) bool { return i%2 == 0 && i >= 40 }, 70)
	if v, err := CheckLOCOMOCandidate(f.plan, []string{f.path}); err != nil || v.Passed || !slices.Contains(v.Failures, "net_loss_beyond_noise") {
		t.Fatal("real loss bought with savings", v, err)
	}
}
