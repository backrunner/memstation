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
	"memstation/apps/server/internal/service/memoryllm"
)

type judgeCalibrationCase struct {
	ID              string `json:"id"`
	Group           string `json:"group"`
	Question        string `json:"question"`
	Reference       string `json:"reference"`
	Candidate       string `json:"candidate"`
	ExpectedCorrect *bool  `json:"expected_correct"`
}

func calibrationPayload(row judgeCalibrationCase) map[string]any {
	return map[string]any{"question": row.Question, "reference_answer": row.Reference, "candidate_answer": row.Candidate}
}

func validateJudgeCalibration(rows []judgeCalibrationCase) error {
	if len(rows) == 0 || len(rows) > 12 {
		return fmt.Errorf("calibration requires 1 to 12 cases")
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.ID == "" || seen[r.ID] || r.Group == "" || strings.TrimSpace(r.Question) == "" || strings.TrimSpace(r.Reference) == "" || strings.TrimSpace(r.Candidate) == "" || r.ExpectedCorrect == nil {
			return fmt.Errorf("invalid calibration case")
		}
		seen[r.ID] = true
	}
	return nil
}

func TestJudgeCalibrationFixtureAndLabelIsolation(t *testing.T) {
	for _, path := range []string{"testdata/judge_calibration.json", "testdata/judge_completeness_calibration.json"} {
		t.Run(filepath.Base(path), func(t *testing.T) { validateCalibrationFixture(t, path) })
	}
}

func validateCalibrationFixture(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	var rows []judgeCalibrationCase
	if err != nil || memoryllm.Decode(raw, &rows) != nil || validateJudgeCalibration(rows) != nil || len(rows) != 12 {
		t.Fatal("invalid independent calibration fixture", err)
	}
	positive := 0
	for _, row := range rows {
		if *row.ExpectedCorrect {
			positive++
		}
		payload := calibrationPayload(row)
		if len(payload) != 3 || payload["id"] != nil || payload["group"] != nil || payload["expected_correct"] != nil {
			t.Fatal("calibration labels leaked to judge")
		}
	}
	if positive != 6 {
		t.Fatal("fixture must include six positive and six negative controls")
	}
	rows[0].ExpectedCorrect = nil
	if validateJudgeCalibration(rows) == nil {
		t.Fatal("missing expected label accepted")
	}
}

// Independent synthetic checks of the existing judge; never rewrites or
// rejudges a benchmark score. IDs, groups and expected labels stay offline.
func TestLiveJudgeCalibration(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_JUDGE_CALIBRATION_CASES")
	if path == "" {
		t.Skip("manual bounded synthetic judge calibration")
	}
	raw, err := os.ReadFile(path)
	var rows []judgeCalibrationCase
	if err != nil || memoryllm.Decode(raw, &rows) != nil || validateJudgeCalibration(rows) != nil || pipelineHash(string(raw)) != os.Getenv("MEMSTATION_EVAL_JUDGE_CALIBRATION_SHA256") {
		t.Fatal("invalid or unfrozen calibration fixture", err)
	}
	judge := pipelineReplayRoleConfig(t, "JUDGE")
	if judge.Model == "" || judge.outputTokenLimit() != 4096 {
		t.Fatal("judge must use a fixed 4096 output ceiling; no retry")
	}
	out := os.Getenv("MEMSTATION_EVAL_JUDGE_CALIBRATION_OUT")
	if out == "" || os.Mkdir(out, 0o700) != nil {
		t.Fatal("fresh calibration output required")
	}
	type result struct {
		Case    judgeCalibrationCase `json:"case"`
		Correct *bool                `json:"correct"`
		Reason  string               `json:"reason"`
		Usage   domain.UsageRecord   `json:"usage"`
		Model   string               `json:"model"`
		Raw     string               `json:"raw"`
		Error   string               `json:"error"`
	}
	results := make([]result, len(rows))
	for i, row := range rows {
		results[i] = result{Case: row, Error: "not executed"}
	}
	calls, tokens := 0, 0
	const tokenLimit = 25000
	manifest := map[string]any{"fixture_sha256": pipelineHash(string(raw)), "judge": judge.runInfo(), "prompt_sha256": pipelineHash(pipelineJudgeInstruction), "call_limit": len(rows), "token_limit": tokenLimit, "scope": "independent synthetic calibration; original benchmark scores immutable"}
	save := func() {
		t.Helper()
		if err := writePipelineJSON(filepath.Join(out, "results.json"), map[string]any{"manifest": manifest, "calls": calls, "reported_tokens": tokens, "results": results}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	save()
	for i, row := range rows {
		payload := calibrationPayload(row)
		encoded, err := json.Marshal(payload)
		if err != nil || calls >= len(rows) || tokens+len(encoded)+len(pipelineJudgeInstruction)+4096+1024 > tokenLimit {
			t.Fatal("calibration budget exhausted; all rows remain denominator")
		}
		results[i].Error = "attempt interrupted"
		calls++
		save()
		response, usage, model, callErr := pipelineCompleteObservedBudget(ctx, judge, pipelineJudgeInstruction, payload, 4096)
		results[i].Raw, results[i].Usage, results[i].Model = string(response), usage, model
		tokens += usage.TotalTokens
		if usage.TotalTokens <= 0 || usage.TotalTokens != usage.InputTokens+usage.OutputTokens || tokens > tokenLimit {
			results[i].Error = "unavailable, inconsistent or excessive usage"
			save()
			t.Fatal(results[i].Error)
		}
		var grade struct {
			Correct *bool  `json:"correct"`
			Reason  string `json:"reason"`
		}
		if callErr != nil {
			results[i].Error = callErr.Error()
		} else if memoryllm.Decode(response, &grade) != nil || grade.Correct == nil || strings.TrimSpace(grade.Reason) == "" {
			results[i].Error = "invalid judgment"
		} else {
			results[i].Correct, results[i].Reason, results[i].Error = grade.Correct, grade.Reason, ""
		}
		save()
		t.Logf("calibrated %d/%d calls=%d tokens=%d error=%t", i+1, len(rows), calls, tokens, results[i].Error != "")
		if results[i].Error != "" {
			t.Errorf("calibration case %s failed", row.ID)
		}
	}
}
