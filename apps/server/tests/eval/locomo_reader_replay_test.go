package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"memstation/apps/server/internal/service/memoryllm"
)

type readerReplayInput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type readerReplayPlan struct {
	Inputs          []readerReplayInput `json:"inputs"`
	Policy          string              `json:"policy"`
	MaxOutputTokens int                 `json:"max_output_tokens"`
}

type readerReplayResult struct {
	Answer     PipelineAnswer             `json:"answer"`
	Reading    *memoryllm.EvidenceReading `json:"reading,omitempty"`
	RawReading string                     `json:"raw_reading,omitempty"`
}

type readerReplayGenerator struct {
	delegate *pipelinePlanGenerator
	raw      string
}

func (g *readerReplayGenerator) Generate(ctx context.Context, req memoryllm.Request) (json.RawMessage, error) {
	raw, err := g.delegate.Generate(ctx, req)
	g.raw = string(raw)
	return raw, err
}

// This manual ablation holds every context byte fixed. It invokes no retrieval,
// extraction, source expansion or database operation and never changes grades in
// the input reports. Every attempted answer, including invalid quotes, persists.
func TestLiveLOCOMOReaderReplay(t *testing.T) {
	planPath := os.Getenv("MEMSTATION_EVAL_READER_REPLAY_PLAN")
	if planPath == "" {
		t.Skip("manual frozen-context reader ablation")
	}
	out := os.Getenv("MEMSTATION_EVAL_READER_REPLAY_OUT")
	revision := os.Getenv("MEMSTATION_EVAL_CODE_REVISION")
	raw, err := os.ReadFile(planPath)
	if err != nil || out == "" || revision == "" {
		t.Fatal("missing reader replay provenance", err)
	}
	var plan readerReplayPlan
	if err = memoryllm.Decode(raw, &plan); err != nil || len(plan.Inputs) == 0 || plan.MaxOutputTokens != 4096 || plan.Policy != "grounded" && plan.Policy != "coverage_inference" && plan.Policy != "interpretation" {
		t.Fatal("invalid reader replay plan", err)
	}
	var inputs []PipelineAnswer
	seen := map[string]bool{}
	for _, source := range plan.Inputs {
		data, err := os.ReadFile(source.Path)
		if err != nil || pipelineHash(string(data)) != source.SHA256 {
			t.Fatal("frozen context report mismatch", err)
		}
		var report LOCOMOPipelineReport
		if err = json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		for _, a := range report.Answers {
			key := a.QuestionID + "|" + a.Mode
			if seen[key] || a.Error != "" || a.ContextHash != pipelineHash(a.Context) || a.ContextBytes != len(a.Context) || len(a.Context) > 16000 {
				t.Fatal("invalid or duplicate frozen context", key)
			}
			seen[key] = true
			inputs = append(inputs, a)
		}
	}
	if err = os.Mkdir(out, 0o700); err != nil {
		t.Fatal("a new complete run requires a fresh directory", err)
	}
	reader, judge := pipelineReplayRoleConfig(t, "ANSWERER"), pipelineReplayRoleConfig(t, "JUDGE")
	if reader.Model == "" || judge.Model == "" {
		t.Fatal("reader and judge must be configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	results := make([]readerReplayResult, len(inputs))
	for i, a := range inputs {
		results[i].Answer = PipelineAnswer{SampleID: a.SampleID, QuestionID: a.QuestionID, Category: a.Category, Mode: a.Mode, Query: a.Query, Expected: a.Expected, EvidenceRefs: a.EvidenceRefs, Context: a.Context, ContextHash: a.ContextHash, ContextBytes: a.ContextBytes, ContextDocuments: a.ContextDocuments, SourceRefs: a.SourceRefs, DiagnosticOnly: a.DiagnosticOnly, Error: "not executed"}
	}
	prompt := pipelineReaderInstruction(plan.Policy)
	if plan.Policy == "grounded" {
		prompt = memoryllm.EvidenceReaderInstruction
	}
	manifest := map[string]any{"code_revision": revision, "plan_sha256": pipelineHash(string(raw)), "inputs": plan.Inputs, "reader_policy": plan.Policy, "reader": reader.runInfo(), "judge": judge.runInfo(), "reader_prompt_sha256": pipelineHash(prompt), "judge_prompt_sha256": pipelineHash(pipelineJudgeInstruction), "max_output_tokens": plan.MaxOutputTokens, "scope": "fixed-context reading only; all cases; developmental cohorts; no retrieval or ingestion claim"}
	manifest["reader_format"] = "plain_text_v1"
	if plan.Policy == "grounded" {
		manifest["reader_format"] = "grounded_json_v1"
	}
	save := func() {
		t.Helper()
		if err := writePipelineJSON(filepath.Join(out, "results.json"), map[string]any{"manifest": manifest, "results": results}); err != nil {
			t.Fatal(err)
		}
	}
	save()
	for i := range results {
		a := &results[i].Answer
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		a.Error = "attempt interrupted before completion"
		save()
		start := time.Now()
		transportUnavailable := false
		if plan.Policy == "grounded" {
			delegate := &pipelinePlanGenerator{config: reader}
			generator := &readerReplayGenerator{delegate: delegate}
			reading, readErr := memoryllm.ReadEvidence(ctx, generator, "reader-replay", a.Query, a.Context, plan.MaxOutputTokens)
			a.AnswerUsage, a.AnswerModel, results[i].RawReading = delegate.usage, delegate.model, generator.raw
			if readErr != nil {
				a.Error = readErr.Error()
				transportUnavailable = errors.Is(readErr, syscall.ECONNREFUSED)
			} else {
				a.Answer, a.Error, results[i].Reading = reading.Answer, "", &reading
			}
		} else {
			answer, usage, model, readErr := pipelineReadObserved(ctx, reader, prompt, map[string]any{"question": a.Query, "memories": a.Context}, plan.MaxOutputTokens)
			a.RawAnswer, results[i].RawReading = answer, answer
			a.AnswerUsage, a.AnswerModel = usage, model
			if readErr != nil {
				a.Error = readErr.Error()
				transportUnavailable = errors.Is(readErr, syscall.ECONNREFUSED)
			} else {
				a.Answer, a.Error = answer, ""
			}
		}
		a.AnswerMillis = float64(time.Since(start).Microseconds()) / 1000
		if a.Error == "" {
			start = time.Now()
			raw, usage, model, gradeErr := pipelineCompleteObserved(ctx, judge, pipelineJudgeInstruction, map[string]any{"question": a.Query, "reference_answer": a.Expected, "candidate_answer": a.Answer})
			a.RawJudgment = string(raw)
			a.JudgeUsage, a.JudgeModel, a.JudgeMillis = usage, model, float64(time.Since(start).Microseconds())/1000
			var grade struct {
				Correct *bool  `json:"correct"`
				Reason  string `json:"reason"`
			}
			if gradeErr != nil {
				a.Error = gradeErr.Error()
				transportUnavailable = errors.Is(gradeErr, syscall.ECONNREFUSED)
			} else if memoryllm.Decode(raw, &grade) != nil || grade.Correct == nil || grade.Reason == "" {
				a.Error = "invalid judge schema"
			} else {
				a.Correct, a.Judged, a.JudgeReason = *grade.Correct, true, grade.Reason
			}
		}
		save()
		t.Logf("%d/%d %s %s correct=%t error=%s", i+1, len(results), a.QuestionID, plan.Policy, a.Correct, a.Error)
		if a.Error != "" {
			t.Errorf("%s: %s", a.QuestionID, a.Error)
			if transportUnavailable {
				t.Fatal("provider connection refused; remaining cohort rows retain not-executed status and stay in the denominator")
			}
		}
	}
}
