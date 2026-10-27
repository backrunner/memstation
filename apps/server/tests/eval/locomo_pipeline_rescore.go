package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"memstation/apps/server/internal/service/memoryllm"
)

// RescoreLOCOMOPipeline preserves generated answers and retrieval artifacts.
// It never calls ingestion or the answerer, and writes a separate report whose
// manifest pins the parent artifact and revised judge protocol.
const pipelineAlternativeJudgeInstruction = pipelineJudgeInstruction + ` Clarification for interpreting reference lists: distinguish an enumeration of jointly required observed facts from alternative acceptable answers to a singular or hypothetical question. When the question asks for one possible explanation, field, outcome or example and the reference supplies alternatives, a candidate that supplies at least one correct alternative is sufficient; do not require every alternative. For questions asking which events, people, attributes or other multiple facts apply, continue requiring all jointly requested reference facts. Decide from the question's meaning, not just commas in the reference. This does not permit an explicit unknown/abstention to match a directional likelihood answer.`

func RescoreLOCOMOPipeline(ctx context.Context, source, output, revision string, judge LongMemEvalQAJudgeOptions) (LOCOMOPipelineReport, error) {
	return RescoreLOCOMOPipelineWithPolicy(ctx, source, output, revision, judge, "reference_v2")
}

func RescoreLOCOMOPipelineWithPolicy(ctx context.Context, source, output, revision string, judge LongMemEvalQAJudgeOptions, policy string) (LOCOMOPipelineReport, error) {
	var report LOCOMOPipelineReport
	instruction := pipelineJudgeInstruction
	protocol := "memstation-locomo-qa-v2-reference-agreement; not a vendor leaderboard reproduction"
	switch policy {
	case "reference_v2":
	case "alternatives_v3":
		instruction = pipelineAlternativeJudgeInstruction
		protocol = "memstation-locomo-qa-v3-reference-alternatives; not a vendor leaderboard reproduction"
	default:
		return report, errors.New("unknown rescore judge policy")
	}
	src, err := filepath.Abs(source)
	if err != nil {
		return report, err
	}
	dst, err := filepath.Abs(output)
	if err != nil {
		return report, err
	}
	if src == dst || strings.TrimSpace(revision) == "" {
		return report, errors.New("rescore requires separate output and source revision")
	}
	if _, err = os.Stat(dst); !os.IsNotExist(err) {
		return report, errors.New("rescore output must not exist")
	}
	if judge, err = normalizeLongMemEvalQAJudgeOptions(judge); err != nil {
		return report, err
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return report, err
	}
	if err = json.Unmarshal(raw, &report); err != nil {
		return report, err
	}
	if len(report.Answers) == 0 || report.CompletedSessions != report.ExpectedSessions {
		return report, errors.New("rescore requires completed ingestion and answers")
	}
	for _, a := range report.Answers {
		if a.Error != "" || !a.Judged || strings.TrimSpace(a.Answer) == "" {
			return report, errors.New("rescore requires a complete parent report")
		}
	}
	if report.Manifest == nil {
		report.Manifest = map[string]any{}
	}
	report.Manifest["parent_report_sha256"] = pipelineHash(string(raw))
	report.Manifest["rescore_code_revision"] = revision
	report.Manifest["judge_protocol"] = protocol
	report.Manifest["rescore_judge_policy"] = policy
	report.Manifest["judge"] = judge.runInfo()
	report.Manifest["judge_prompt_sha256"] = pipelineHash(instruction)
	report.Manifest["rescore_judge_endpoint_sha256"] = pipelineHash(judge.BaseURL)
	report.GeneratedAt = time.Now().UTC()
	for i := range report.Answers {
		a := &report.Answers[i]
		a.Judged = false
		a.Correct = false
		a.Error = "not rescored"
		a.JudgeReason = ""
		a.JudgeMillis = 0
		a.JudgeUsage.InputTokens = 0
		a.JudgeUsage.OutputTokens = 0
		a.JudgeUsage.TotalTokens = 0
	}
	save := func() error {
		refreshPipelineSummaries(&report)
		return writePipelineJSON(dst, report)
	}
	if err = save(); err != nil {
		return report, err
	}
	var failures []error
	for i := range report.Answers {
		a := &report.Answers[i]
		start := time.Now()
		raw, usage, model, callErr := pipelineCompleteObserved(ctx, judge, instruction, map[string]any{"question": a.Query, "reference_answer": a.Expected, "candidate_answer": a.Answer})
		a.JudgeUsage = usage
		a.JudgeModel = model
		a.JudgeMillis = float64(time.Since(start).Microseconds()) / 1000
		var grade struct {
			Correct *bool  `json:"correct"`
			Reason  string `json:"reason"`
		}
		if callErr == nil {
			callErr = memoryllm.Decode(raw, &grade)
		}
		if callErr == nil && (grade.Correct == nil || strings.TrimSpace(grade.Reason) == "") {
			callErr = errors.New("invalid judge schema")
		}
		if callErr != nil {
			a.Error = callErr.Error()
			failures = append(failures, fmt.Errorf("%s %s: judge failed", a.QuestionID, a.Mode))
		} else {
			a.Error = ""
			a.Judged = true
			a.Correct = *grade.Correct
			a.JudgeReason = grade.Reason
		}
		if err = save(); err != nil {
			return report, err
		}
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
	}
	return report, errors.Join(failures...)
}
