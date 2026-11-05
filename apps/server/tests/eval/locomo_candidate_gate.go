package eval

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"memstation/apps/server/internal/domain"
)

// Candidate acceptance is offline. No plan, label or historical score is passed
// to a provider, retriever or production request. All selected rows are retained.
type LOCOMOCandidatePlan struct {
	Version               int              `json:"version"`
	Purpose               string           `json:"purpose"` // screen, replacement or cost_reduction
	CandidateCodeRevision string           `json:"candidate_code_revision"`
	Baselines             []LOCOMOArtifact `json:"baselines"`
	RepeatBaselines       []LOCOMOArtifact `json:"repeat_baselines"` // same code and protocol, rerun; measures the noise floor
	// DeclaredRefinement admits a candidate that spends extra planner calls. The
	// baseline stays without refinement, and planner tokens count against the budget.
	DeclaredRefinement bool `json:"declared_refinement,omitempty"`
	// DeclaredContextBytes admits a larger context budget, which is more evidence,
	// not a same-budget retrieval gain; the token ratio still bounds it.
	DeclaredContextBytes     int             `json:"declared_context_bytes,omitempty"`
	Cases                    []LOCOMOCaseKey `json:"cases"`
	MaxAnswerInputTokenRatio float64         `json:"max_answer_input_token_ratio"`
	MaxQATotalTokens         int64           `json:"max_qa_total_tokens"`
}

type LOCOMOArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type LOCOMOCaseKey struct {
	SampleID   string `json:"sample_id"`
	QuestionID string `json:"question_id"`
	Mode       string `json:"mode"`
}

type LOCOMOCandidateCase struct {
	LOCOMOCaseKey
	BaselineCorrect  bool     `json:"baseline_correct"`
	CandidateCorrect bool     `json:"candidate_correct"`
	Failures         []string `json:"failures"`
}

type LOCOMOCandidateVerdict struct {
	PlanSHA256             string                `json:"plan_sha256"`
	CandidateCodeRevision  string                `json:"candidate_code_revision"`
	Purpose                string                `json:"purpose"`
	Passed                 bool                  `json:"passed"`
	ReplacementEligible    bool                  `json:"replacement_eligible"`
	Expected               int                   `json:"expected"`
	BaselineCorrect        int                   `json:"baseline_correct"`
	Gains                  int                   `json:"gains"`
	Regressions            int                   `json:"regressions"`
	NetGain                int                   `json:"net_gain"`
	McNemarP               float64               `json:"mcnemar_p"`
	NoiseFlips             int                   `json:"noise_flips"`
	NoiseAbsNet            int                   `json:"noise_abs_net"`
	NoiseRegressionRate    float64               `json:"noise_regression_rate"`
	RegressionExcessP      float64               `json:"regression_excess_p"`
	Categories             []LOCOMOCategoryDelta `json:"categories"`
	BaselineInputTokens    int64                 `json:"baseline_answer_input_tokens"`
	CandidateInputTokens   int64                 `json:"candidate_answer_input_tokens"`
	CandidateQATotalTokens int64                 `json:"candidate_qa_reported_tokens"`
	Failures               []string              `json:"failures"`
	Cases                  []LOCOMOCandidateCase `json:"cases"`
	Baselines              []LOCOMOArtifact      `json:"baselines"`
	Candidates             []LOCOMOArtifact      `json:"candidates"`
	Scope                  string                `json:"scope"`
}

// LOCOMOCategoryDelta reports paired changes by category. A category fails only
// when its losses are statistically unlikely under symmetric noise.
type LOCOMOCategoryDelta struct {
	Category    int     `json:"category"`
	Cases       int     `json:"cases"`
	Gains       int     `json:"gains"`
	Regressions int     `json:"regressions"`
	LossP       float64 `json:"loss_p"`
}

const (
	// Replacement evidence needs enough paired questions to detect a real change;
	// a single question must not be able to decide the outcome.
	locomoGateMinReplacementCases = 200
	locomoGateMinCategoryCases    = 15
	locomoGateAlpha               = 0.05
)

// binomialUpperTail returns P(X >= k) for X ~ Binomial(n, p).
func binomialUpperTail(n, k int, p float64) float64 {
	if k <= 0 {
		return 1
	}
	if k > n || p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	logCombination := func(i int) float64 {
		a, _ := math.Lgamma(float64(n + 1))
		b, _ := math.Lgamma(float64(i + 1))
		c, _ := math.Lgamma(float64(n - i + 1))
		return a - b - c
	}
	total := 0.0
	for i := k; i <= n; i++ {
		total += math.Exp(logCombination(i) + float64(i)*math.Log(p) + float64(n-i)*math.Log1p(-p))
	}
	return math.Min(total, 1)
}

// exactMcNemarP is the two-sided exact test on discordant pairs. Symmetric
// reader and judge noise produces discordance without shifting this statistic.
func exactMcNemarP(gains, regressions int) float64 {
	n := gains + regressions
	if n == 0 {
		return 1
	}
	return math.Min(1, 2*binomialUpperTail(n, max(gains, regressions), .5))
}

func regressionFailures(baseline, candidate PipelineAnswer) []string {
	failures := candidatePairFailures(baseline, candidate)
	if baseline.Correct && !candidate.Correct {
		failures = append(failures, "answer_regression")
	}
	if !baseline.Judged || baseline.Error != "" || !baseline.Correct {
		failures = append(failures, "invalid_protected_baseline")
	}
	return failures
}

func candidatePairFailures(baseline, candidate PipelineAnswer) []string {
	var failures []string
	if baseline.QuestionID == "" || baseline.QuestionID != candidate.QuestionID || baseline.SampleID != candidate.SampleID || baseline.Query != candidate.Query || baseline.Expected != candidate.Expected || baseline.Category != candidate.Category || baseline.Mode != candidate.Mode {
		failures = append(failures, "question_identity")
	}
	if !candidate.Judged || candidate.Error != "" {
		failures = append(failures, "execution_incomplete")
	}
	if baseline.ContextHash != pipelineHash(baseline.Context) || candidate.ContextHash != pipelineHash(candidate.Context) {
		failures = append(failures, "context_identity")
	}
	if baseline.AnswerModel != candidate.AnswerModel || baseline.JudgeModel != candidate.JudgeModel {
		failures = append(failures, "returned_model_changed")
	}
	return failures
}

func candidateKey(a PipelineAnswer) LOCOMOCaseKey {
	return LOCOMOCaseKey{a.SampleID, a.QuestionID, a.Mode}
}

// The two current report producers use distinct prompt fields. These are
// explicit protocol adapters, not a search for whichever alias happens to exist.
func candidateProtocol(m map[string]any) (map[string]any, error) {
	readerKey := ""
	switch {
	case m["version"] == float64(4):
		readerKey = "answer_prompt_sha256"
	case m["experiment"] == "current_engine_frozen_corpus_evidence_replay":
		readerKey = "reader_prompt_sha256"
	default:
		return nil, fmt.Errorf("unsupported report protocol")
	}
	out := map[string]any{}
	for _, key := range []string{"dataset_sha256", "answerer", "judge", "embedding", "processing", "rerank", "top_n", "reader_policy", "reader_format", "context_format", "context_byte_budget", "source_neighbor_turns", "qa_output_tokens", "judge_initial_output_tokens", "judge_prompt_sha256", "refine_evidence", "image_policy", "judge_protocol", "embedding_revision"} {
		v, ok := m[key]
		if !ok || v == nil {
			return nil, fmt.Errorf("missing protocol field %s", key)
		}
		// The route to a model is not part of the protocol; its identity and settings are.
		if role, isRole := v.(map[string]any); isRole {
			role = maps.Clone(role)
			delete(role, "base_url")
			v = role
		}
		out[key] = v
	}
	prompt, ok := m[readerKey].(string)
	if !ok || !candidateSHA256(prompt) {
		return nil, fmt.Errorf("missing reader prompt identity")
	}
	out["reader_prompt"] = prompt
	return out, nil
}

type candidateReport struct {
	report   LOCOMOPipelineReport
	manifest string
	artifact string
	protocol map[string]any
	issue    string
}

type candidateRow struct {
	answer PipelineAnswer
	report *candidateReport
}

func candidateSHA256(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32
}

func readCandidateJSON(path string, output any) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(output); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return raw, nil
}

func loadCandidateReports(artifacts []LOCOMOArtifact, pinned bool) (map[LOCOMOCaseKey]candidateRow, []LOCOMOArtifact, []string) {
	rows := map[LOCOMOCaseKey]candidateRow{}
	var loaded []LOCOMOArtifact
	var issues []string
	for i, artifact := range artifacts {
		r := &candidateReport{}
		raw, err := os.ReadFile(artifact.Path)
		if err != nil || json.Unmarshal(raw, &r.report) != nil || pinned && (!candidateSHA256(artifact.SHA256) || pipelineHash(string(raw)) != artifact.SHA256) {
			issues = append(issues, fmt.Sprintf("artifact_%d_invalid", i))
			continue
		}
		loaded = append(loaded, LOCOMOArtifact{artifact.Path, pipelineHash(string(raw))})
		r.artifact = pipelineHash(string(raw))
		// Producers hash their compact serialized manifest. Typed nested structs
		// have declaration order, which a map roundtrip would silently reorder.
		var envelope struct {
			Manifest json.RawMessage `json:"manifest"`
		}
		var compact bytes.Buffer
		if json.Unmarshal(raw, &envelope) != nil || json.Compact(&compact, envelope.Manifest) != nil {
			issues = append(issues, fmt.Sprintf("artifact_%d_manifest_invalid", i))
			continue
		}
		r.manifest = pipelineHash(compact.String())
		r.protocol, err = candidateProtocol(r.report.Manifest)
		if err != nil {
			r.issue = err.Error()
			issues = append(issues, fmt.Sprintf("artifact_%d_protocol: %s", i, r.issue))
		}
		if r.report.ExpectedSessions <= 0 || r.report.ExpectedSessions != r.report.CompletedSessions {
			issues = append(issues, fmt.Sprintf("artifact_%d_ingestion_incomplete", i))
		}
		for _, stage := range r.report.Stages {
			if stage.Error != "" {
				issues = append(issues, fmt.Sprintf("artifact_%d_ingestion_error", i))
				break
			}
		}
		for _, row := range r.report.Answers {
			key := candidateKey(row)
			if _, exists := rows[key]; exists {
				issues = append(issues, "duplicate_answer")
				continue
			}
			rows[key] = candidateRow{row, r}
		}
	}
	return rows, loaded, issues
}

func candidateUsageValid(u domain.UsageRecord) bool {
	return u.InputTokens > 0 && u.OutputTokens >= 0 && u.TotalTokens > 0 && u.TotalTokens == u.InputTokens+u.OutputTokens
}

func candidateRowFailures(row candidateRow) []string {
	a := row.answer
	var failures []string
	if !a.Judged || a.Error != "" || a.Answer == "" || a.AnswerModel == "" || a.JudgeModel == "" {
		failures = append(failures, "execution_incomplete")
	}
	if !pipelineEntityRecallComplete(a.EntityRecall) {
		failures = append(failures, "entity_recall_incomplete")
	}
	if a.ManifestHash != row.report.manifest || a.ContextHash != pipelineHash(a.Context) || a.ContextBytes != len(a.Context) {
		failures = append(failures, "artifact_identity")
	}
	if limit, ok := row.report.report.Manifest["context_byte_budget"].(float64); !ok || limit <= 0 || float64(a.ContextBytes) > limit {
		failures = append(failures, "context_budget_exceeded")
	}
	if a.DiagnosticOnly || pipelineControlMode(a.Mode) {
		failures = append(failures, "oracle_or_diagnostic_answer")
	}
	if !candidateUsageValid(a.AnswerUsage) || !candidateUsageValid(a.JudgeUsage) {
		failures = append(failures, "usage_missing_or_inconsistent")
	}
	if a.PlannerUsage.TotalTokens != 0 || a.PlannerModel != "" {
		failures = append(failures, "extra_generation")
	}
	return failures
}

// CheckLOCOMOCandidate reads immutable reports only. It never rejudges, retries,
// drops an error row, or loads the original dataset into a model.
func CheckLOCOMOCandidate(plan LOCOMOCandidatePlan, candidates []string) (LOCOMOCandidateVerdict, error) {
	v := LOCOMOCandidateVerdict{Purpose: plan.Purpose, CandidateCodeRevision: plan.CandidateCodeRevision, Expected: len(plan.Cases), Scope: "frozen cohort only; paired test against a measured noise floor; reported QA tokens exclude ingestion and unreported provider attempts; no unseen/full-benchmark or deployment claim"}
	if plan.Version != 2 || (plan.Purpose != "screen" && plan.Purpose != "replacement" && plan.Purpose != "cost_reduction") || len(plan.RepeatBaselines) == 0 || len(plan.RepeatBaselines) > 64 || plan.CandidateCodeRevision == "" || len(plan.Cases) == 0 || len(plan.Cases) > 2000 || len(plan.Baselines) == 0 || len(plan.Baselines) > 64 || len(candidates) == 0 || len(candidates) > 64 || math.IsNaN(plan.MaxAnswerInputTokenRatio) || math.IsInf(plan.MaxAnswerInputTokenRatio, 0) || plan.MaxAnswerInputTokenRatio <= 0 || plan.MaxQATotalTokens <= 0 {
		return v, fmt.Errorf("invalid frozen candidate plan")
	}
	seen := map[LOCOMOCaseKey]bool{}
	for _, key := range plan.Cases {
		if key.SampleID == "" || key.QuestionID == "" || (key.Mode != "raw" && key.Mode != "derived" && key.Mode != "combined") || seen[key] {
			return v, fmt.Errorf("invalid or duplicate frozen case")
		}
		seen[key] = true
	}
	before, baselineArtifacts, issues := loadCandidateReports(plan.Baselines, true)
	v.Baselines = baselineArtifacts
	if len(issues) != 0 {
		return v, fmt.Errorf("invalid frozen baseline: %v", issues)
	}
	repeats, _, issues := loadCandidateReports(plan.RepeatBaselines, true)
	if len(issues) != 0 {
		return v, fmt.Errorf("invalid repeat baseline: %v", issues)
	}
	noiseRegressions, noiseCorrect, noiseNet := 0, 0, 0
	for _, key := range plan.Cases {
		b, ok := before[key]
		r, repeated := repeats[key]
		if !ok || !repeated {
			return v, fmt.Errorf("frozen case %s is missing from a baseline or its repeat", key.QuestionID)
		}
		if failures := candidateRowFailures(r); len(failures) != 0 {
			return v, fmt.Errorf("invalid repeat baseline case %s: %v", key.QuestionID, failures)
		}
		if len(candidatePairFailures(b.answer, r.answer)) != 0 || r.report.issue != "" || !reflect.DeepEqual(b.report.protocol, r.report.protocol) || r.report.report.Manifest["code_revision"] != b.report.report.Manifest["code_revision"] {
			return v, fmt.Errorf("repeat baseline case %s is not the same code and protocol", key.QuestionID)
		}
		if b.answer.Correct != r.answer.Correct {
			v.NoiseFlips++
			if b.answer.Correct {
				noiseRegressions++
				noiseNet--
			} else {
				noiseNet++
			}
		}
		if b.answer.Correct {
			noiseCorrect++
		}
	}
	v.NoiseAbsNet = max(noiseNet, -noiseNet)
	if noiseCorrect > 0 {
		// A zero-flip repeat cannot prove zero noise; floor at one case's resolution.
		v.NoiseRegressionRate = math.Max(float64(noiseRegressions), 1) / float64(noiseCorrect)
	}
	var candidateArtifacts []LOCOMOArtifact
	for _, path := range candidates {
		candidateArtifacts = append(candidateArtifacts, LOCOMOArtifact{Path: path})
	}
	after, candidateArtifacts, issues := loadCandidateReports(candidateArtifacts, false)
	v.Candidates, v.Failures = candidateArtifacts, issues
	categories := map[int]*LOCOMOCategoryDelta{}
	for _, key := range plan.Cases {
		b, ok := before[key]
		if !ok {
			return v, fmt.Errorf("missing frozen baseline case %s", key.QuestionID)
		}
		if b.report.protocol["refine_evidence"] != false {
			return v, fmt.Errorf("baseline case %s must not use refinement", key.QuestionID)
		}
		if failures := candidateRowFailures(b); len(failures) != 0 {
			return v, fmt.Errorf("invalid frozen baseline case %s: %v", key.QuestionID, failures)
		}
		if b.answer.Correct {
			v.BaselineCorrect++
		}
		category, known := categories[b.answer.Category]
		if !known {
			category = &LOCOMOCategoryDelta{Category: b.answer.Category}
			categories[b.answer.Category] = category
		}
		category.Cases++
		v.BaselineInputTokens += int64(b.answer.AnswerUsage.InputTokens)
		row := LOCOMOCandidateCase{LOCOMOCaseKey: key, BaselineCorrect: b.answer.Correct}
		c, exists := after[key]
		if !exists {
			row.Failures = append(row.Failures, "missing_answer")
		} else {
			row.CandidateCorrect = c.answer.Correct && c.answer.Judged && c.answer.Error == ""
			row.Failures = append(row.Failures, candidatePairFailures(b.answer, c.answer)...)
			for _, failure := range candidateRowFailures(c) {
				if failure != "extra_generation" || !plan.DeclaredRefinement {
					row.Failures = append(row.Failures, failure)
				}
			}
			protocol := maps.Clone(c.report.protocol)
			if plan.DeclaredRefinement && protocol != nil {
				protocol["refine_evidence"] = false
			}
			if plan.DeclaredContextBytes > 0 && protocol != nil && protocol["context_byte_budget"] == float64(plan.DeclaredContextBytes) {
				protocol["context_byte_budget"] = b.report.protocol["context_byte_budget"]
			}
			if c.report.issue != "" || !reflect.DeepEqual(b.report.protocol, protocol) {
				row.Failures = append(row.Failures, "protocol_changed")
			}
			if c.report.report.Manifest["code_revision"] != plan.CandidateCodeRevision {
				row.Failures = append(row.Failures, "candidate_revision_changed")
			}
			if c.report.report.ExpectedSessions != b.report.report.ExpectedSessions {
				row.Failures = append(row.Failures, "source_session_count_changed")
			}
			if c.report.report.Manifest["experiment"] == "current_engine_frozen_corpus_evidence_replay" {
				snapshot, _ := c.report.report.Manifest["database_snapshot_sha256"].(string)
				if c.report.report.Manifest["parent_sha256"] != b.report.artifact || !candidateSHA256(snapshot) || c.report.report.Manifest["read_only_database"] != true {
					row.Failures = append(row.Failures, "replay_corpus_identity")
				}
			}
			v.CandidateInputTokens += int64(c.answer.AnswerUsage.InputTokens)
			v.CandidateQATotalTokens += int64(c.answer.AnswerUsage.TotalTokens) + int64(c.answer.JudgeUsage.TotalTokens) + int64(c.answer.PlannerUsage.TotalTokens)
		}
		if b.answer.Correct && !row.CandidateCorrect {
			v.Regressions++
			category.Regressions++
		}
		if !b.answer.Correct && row.CandidateCorrect {
			v.Gains++
			category.Gains++
		}
		slices.Sort(row.Failures)
		row.Failures = slices.Compact(row.Failures)
		v.Cases = append(v.Cases, row)
	}
	v.NetGain = v.Gains - v.Regressions
	v.McNemarP = exactMcNemarP(v.Gains, v.Regressions)
	v.RegressionExcessP = binomialUpperTail(v.BaselineCorrect, v.Regressions, v.NoiseRegressionRate)
	if v.BaselineCorrect > 0 && v.RegressionExcessP < locomoGateAlpha {
		v.Failures = append(v.Failures, "regressions_exceed_noise")
	}
	for _, id := range slices.Sorted(maps.Keys(categories)) {
		c := categories[id]
		c.LossP = binomialUpperTail(c.Gains+c.Regressions, c.Regressions, .5)
		if c.Regressions > c.Gains && c.LossP < locomoGateAlpha {
			v.Failures = append(v.Failures, fmt.Sprintf("category_%d_regression", id))
		}
		if plan.Purpose != "screen" && c.Cases < locomoGateMinCategoryCases {
			v.Failures = append(v.Failures, fmt.Sprintf("category_%d_underpowered", id))
		}
		v.Categories = append(v.Categories, *c)
	}
	if float64(v.CandidateInputTokens) > float64(v.BaselineInputTokens)*plan.MaxAnswerInputTokenRatio || v.CandidateQATotalTokens > plan.MaxQATotalTokens {
		v.Failures = append(v.Failures, "qa_token_budget_exceeded")
	}
	if plan.Purpose == "cost_reduction" {
		// A cheaper candidate needs no gain: it must spend less and lose no more
		// answers than the same code loses when simply run again.
		if v.CandidateInputTokens >= v.BaselineInputTokens {
			v.Failures = append(v.Failures, "no_token_reduction")
		}
		if v.NetGain < -v.NoiseAbsNet {
			v.Failures = append(v.Failures, "net_loss_beyond_noise")
		}
		if v.NetGain < 0 && v.McNemarP < locomoGateAlpha {
			v.Failures = append(v.Failures, "significant_net_loss")
		}
	} else if v.NetGain <= v.NoiseAbsNet {
		v.Failures = append(v.Failures, "net_gain_within_noise")
	}
	if plan.Purpose != "screen" && len(plan.Cases) < locomoGateMinReplacementCases {
		v.Failures = append(v.Failures, "too_few_cases_for_replacement")
	}
	if plan.Purpose == "replacement" && v.McNemarP >= locomoGateAlpha {
		v.Failures = append(v.Failures, "gain_not_significant")
	}
	v.Passed = len(v.Failures) == 0
	for _, row := range v.Cases {
		v.Passed = v.Passed && len(row.Failures) == 0
	}
	v.ReplacementEligible = v.Passed && plan.Purpose != "screen"
	return v, nil
}

// RunLOCOMOCandidateGate requires a separately pinned plan hash and fresh output.
// A receipt is created only on success, and binds the exact reports and code.
// It certifies this offline check, not a deployed version or future source edits.
func RunLOCOMOCandidateGate(planPath, planSHA256, candidatesPath, out string) (LOCOMOCandidateVerdict, error) {
	var plan LOCOMOCandidatePlan
	raw, err := readCandidateJSON(planPath, &plan)
	if err != nil || !candidateSHA256(planSHA256) || pipelineHash(string(raw)) != planSHA256 {
		return LOCOMOCandidateVerdict{}, fmt.Errorf("frozen plan identity mismatch")
	}
	var candidates []string
	if _, err = readCandidateJSON(candidatesPath, &candidates); err != nil {
		return LOCOMOCandidateVerdict{}, fmt.Errorf("invalid candidate report list")
	}
	v, err := CheckLOCOMOCandidate(plan, candidates)
	if err != nil {
		return v, err
	}
	v.PlanSHA256 = planSHA256
	if err = os.Mkdir(out, 0o700); err != nil {
		return v, fmt.Errorf("gate requires fresh output: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return v, err
	}
	if err = os.WriteFile(filepath.Join(out, "verdict.json"), data, 0o600); err != nil {
		return v, err
	}
	if !v.Passed {
		return v, fmt.Errorf("candidate rejected: net %d, p=%.4f, failures %v; inspect verdict.json", v.NetGain, v.McNemarP, v.Failures)
	}
	receipt := map[string]any{"created_at": time.Now().UTC(), "verdict_sha256": pipelineHash(string(data)), "plan_sha256": planSHA256, "candidate_code_revision": plan.CandidateCodeRevision, "purpose": plan.Purpose, "replacement_eligible": v.ReplacementEligible, "candidates": v.Candidates}
	data, err = json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return v, err
	}
	err = os.WriteFile(filepath.Join(out, "acceptance.json"), data, 0o600)
	return v, err
}
