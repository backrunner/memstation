package eval

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/memoryllm"
)

type judgeProbePlan struct {
	Reports      []string `json:"reports"`
	System       string   `json:"system"`
	UserTemplate string   `json:"user_template"`
	SourceURL    string   `json:"source_url"`
	SourceSHA256 string   `json:"source_sha256"`
}

type judgeProbeRow struct {
	Report        string             `json:"report"`
	ParentSHA256  string             `json:"parent_sha256"`
	QuestionID    string             `json:"question_id"`
	AnswerSHA256  string             `json:"answer_sha256"`
	ContextSHA256 string             `json:"context_sha256"`
	Before        bool               `json:"before"`
	Correct       bool               `json:"correct"`
	Judged        bool               `json:"judged"`
	Error         string             `json:"error,omitempty"`
	Model         string             `json:"model,omitempty"`
	Millis        float64            `json:"millis"`
	Usage         domain.UsageRecord `json:"usage"`
	RawOutput     string             `json:"raw_output,omitempty"`
}

func parseJudgeProbe(raw string) (bool, bool) {
	var result struct {
		Label string `json:"label"`
	}
	// The inspected vendor runner permits explanatory prose before the JSON,
	// code fences, extra keys and label case/whitespace. Preserve that protocol
	// only here; production memory extraction keeps its stricter decoder.
	for _, pattern := range []string{"(?s)```(?:json)?\\s*(\\{[^`]*\\})\\s*```", `(?s)(\{[^{}]*"label"\s*:\s*"[^"]*"[^{}]*\})`} {
		if match := regexp.MustCompile(pattern).FindStringSubmatch(raw); len(match) > 1 {
			raw = match[1]
			break
		}
	}
	if json.Unmarshal([]byte(raw), &result) != nil {
		return false, false
	}
	label := strings.ToUpper(strings.TrimSpace(result.Label))
	return label == "CORRECT", label == "CORRECT" || label == "WRONG"
}

func TestJudgeProbeLabelCompatibility(t *testing.T) {
	for _, tt := range []struct {
		raw            string
		correct, valid bool
	}{
		{`{"label":" correct ","reason":"equivalent"}`, true, true},
		{`{"label":"WRONG"}`, false, true},
		{`{"reason":"CORRECT"}`, false, false},
		{`{"label":"NOT CORRECT"}`, false, false},
		{`{"label":true}`, false, false},
		{`Explanation. {"label":"CORRECT"} trailing`, true, true},
		{"```json\n{\"label\":\"WRONG\"}\n```", false, true},
	} {
		correct, valid := parseJudgeProbe(tt.raw)
		if correct != tt.correct || valid != tt.valid {
			t.Fatal(tt.raw, correct, valid)
		}
	}
}

// Reparse saved provider output without making any model call or replacing an
// earlier artifact. This isolates decoder compatibility from scoring changes.
func TestLOCOMOJudgeProbeReparse(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_JUDGE_PROBE_REPARSE")
	if path == "" {
		t.Skip("manual saved judge output reparse")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]json.RawMessage
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	var rows []judgeProbeRow
	if err = json.Unmarshal(report["rows"], &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("empty probe")
	}
	for i := range rows {
		r := &rows[i]
		if r.Error != "" && r.Error != "invalid judge label" {
			continue
		}
		r.Correct, r.Judged = parseJudgeProbe(r.RawOutput)
		if !r.Judged {
			r.Error = "invalid judge label"
		} else {
			r.Error = ""
		}
	}
	report["rows"], err = json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	report["parent_probe_sha256"], _ = json.Marshal(pipelineHash(string(raw)))
	report["parser_protocol"], _ = json.Marshal("EverOS 5b0979b benchmarks/run.py JSON extraction and label normalization; no additional calls")
	out := os.Getenv("MEMSTATION_EVAL_JUDGE_PROBE_OUT")
	f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = writePipelineJSON(out, report); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if !r.Judged {
			t.Errorf("%s: %s", r.QuestionID, r.Error)
		}
	}
}

// Replace all placeholders in one pass: source text containing a placeholder
// must remain data and must not be rewritten by a later substitution.
func renderJudgeProbe(template string, a PipelineAnswer) string {
	return strings.NewReplacer("{question}", a.Query, "{golden_answer}", a.Expected, "{generated_answer}", a.Answer).Replace(template)
}

func TestJudgeProbeRenderingDoesNotRecursivelySubstitute(t *testing.T) {
	a := PipelineAnswer{Query: "literal {generated_answer}", Expected: "GOLD", Answer: "candidate {golden_answer}"}
	got := renderJudgeProbe("{question}|{golden_answer}|{generated_answer}", a)
	if got != "literal {generated_answer}|GOLD|candidate {golden_answer}" {
		t.Fatal(got)
	}
}

// Manual protocol sensitivity diagnostic. No ingestion, retrieval or answering;
// only completed frozen answers are sent to an explicitly supplied judge prompt.
// Output is reserved before calls and cannot be resumed or overwritten.
func TestLOCOMOFrozenJudgeProtocolProbe(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_JUDGE_PROBE_PLAN")
	if path == "" {
		t.Skip("manual external judge protocol diagnostic")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var plan judgeProbePlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Reports) == 0 || plan.System == "" || plan.SourceURL == "" || len(plan.SourceSHA256) != 64 {
		t.Fatal("incomplete probe plan")
	}
	for _, key := range []string{"{question}", "{golden_answer}", "{generated_answer}"} {
		if strings.Count(plan.UserTemplate, key) != 1 {
			t.Fatal("invalid template placeholder", key)
		}
	}
	cfg, err := normalizeLongMemEvalQAJudgeOptions(LongMemEvalQAJudgeOptions{Model: os.Getenv("JUDGE_MODEL"), BaseURL: os.Getenv("JUDGE_BASE_URL"), APIKey: os.Getenv("JUDGE_API_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	rows := []judgeProbeRow{}
	answers := []PipelineAnswer{}
	seen := map[string]bool{}
	for _, src := range plan.Reports {
		if seen[src] {
			t.Fatal("duplicate parent report")
		}
		seen[src] = true
		data, readErr := os.ReadFile(src)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var r LOCOMOPipelineReport
		if err = json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Answers) == 0 || r.CompletedSessions != r.ExpectedSessions {
			t.Fatal("incomplete parent")
		}
		ids := map[string]bool{}
		for _, a := range r.Answers {
			key := a.QuestionID + ":" + a.Mode
			if !a.Judged || a.Error != "" || a.Answer == "" || ids[key] {
				t.Fatal("invalid frozen answer")
			}
			ids[key] = true
			rows = append(rows, judgeProbeRow{Report: src, ParentSHA256: pipelineHash(string(data)), QuestionID: a.QuestionID, AnswerSHA256: pipelineHash(a.Answer), ContextSHA256: pipelineHash(a.Context), Before: a.Correct, Error: "not executed"})
			answers = append(answers, a)
		}
	}
	out := os.Getenv("MEMSTATION_EVAL_JUDGE_PROBE_OUT")
	f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	report := struct {
		PlanSHA256     string          `json:"plan_sha256"`
		SourceURL      string          `json:"source_url"`
		SourceSHA256   string          `json:"source_sha256"`
		JudgeModel     string          `json:"judge_model"`
		EndpointSHA256 string          `json:"endpoint_sha256"`
		Rows           []judgeProbeRow `json:"rows"`
	}{pipelineHash(string(raw)), plan.SourceURL, plan.SourceSHA256, cfg.Model, pipelineHash(cfg.BaseURL), rows}
	save := func() {
		t.Helper()
		if e := writePipelineJSON(out, report); e != nil {
			t.Fatal(e)
		}
	}
	save()
	for i, a := range answers {
		row := &report.Rows[i]
		row.Error = "attempt interrupted"
		save()
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		start := time.Now()
		_, model := memoryllm.ModelReference(cfg.Model)
		resp, callErr := memoryllm.CompleteWithBudget(ctx, pipelineAdapter(cfg), domain.UnifiedRequest{Model: model, Protocol: domain.ProtocolOpenAIChat, Provider: domain.ProviderOpenAICompatible, Mode: domain.ModeTransparent, JSONOutput: true, MaxOutputTokens: 4096, Messages: []domain.UnifiedMessage{{Role: domain.RoleSystem, Text: plan.System}, {Role: domain.RoleUser, Text: renderJudgeProbe(plan.UserTemplate, a)}}}, cfg.outputTokenLimit())
		cancel()
		row.Millis = float64(time.Since(start).Microseconds()) / 1000
		if resp != nil {
			row.Usage, row.Model = resp.Usage, resp.Model
			row.RawOutput = resp.Message.Text
		}
		correct, valid := parseJudgeProbe(row.RawOutput)
		if callErr != nil || resp == nil {
			row.Error = "judge call failed"
		} else if !valid {
			row.Error = "invalid judge label"
		} else {
			row.Correct, row.Judged, row.Error = correct, true, ""
		}
		save()
		if row.Error != "" {
			t.Errorf("%s: %s", a.QuestionID, row.Error)
		}
	}
}
