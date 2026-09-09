package eval

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBuildLongMemEvalQAPromptKnowledgeUpdate(t *testing.T) {
	t.Parallel()

	prompt, err := buildLongMemEvalQAPrompt(longMemEvalSample{
		QuestionID:   "q001",
		QuestionType: "knowledge-update",
		Question:     "Where does the user live now?",
		Answer:       "Portland",
	}, "The user used to live in Seattle but now lives in Portland.")
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	if !strings.Contains(prompt, "updated answer") {
		t.Fatalf("expected knowledge-update guidance in prompt, got: %s", prompt)
	}
}

func TestLongMemEvalQARejectsDuplicateIDsBeforeJudgingAndReportsCoverage(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "yes"}}}})
	}))
	defer server.Close()
	options := LongMemEvalQAJudgeOptions{Model: "fixture-judge", BaseURL: server.URL, APIKey: "fixture"}
	refs := []longMemEvalSample{
		{QuestionID: "a", QuestionType: "single-session-user", Question: "Where?", Answer: "Paris"},
		{QuestionID: "b", QuestionType: "single-session-user", Question: "Who?", Answer: "Sam"},
	}
	hyp := LongMemEvalQAHypothesis{QuestionID: "a", Hypothesis: "Paris"}
	if _, err := evaluateLongMemEvalQAWithHypotheses(context.Background(), refs, []LongMemEvalQAHypothesis{hyp, hyp}, options); err == nil {
		t.Fatal("duplicate hypotheses accepted")
	}
	if _, err := evaluateLongMemEvalQAWithHypotheses(context.Background(), []longMemEvalSample{refs[0], refs[0]}, []LongMemEvalQAHypothesis{hyp}, options); err == nil {
		t.Fatal("duplicate references accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid input reached paid judge")
	}
	evaluation, err := evaluateLongMemEvalQAWithHypotheses(context.Background(), refs, []LongMemEvalQAHypothesis{hyp}, options)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Summary.Coverage != 0.5 || evaluation.Summary.ReferenceCount != 2 || len(evaluation.Summary.MissingQuestionIDs) != 1 || evaluation.Summary.MissingQuestionIDs[0] != "b" {
		t.Fatalf("incomplete coverage is not visible: %+v", evaluation.Summary)
	}
}

func TestBuildLongMemEvalQAPromptAbstention(t *testing.T) {
	t.Parallel()

	prompt, err := buildLongMemEvalQAPrompt(longMemEvalSample{
		QuestionID:   "q003_abs",
		QuestionType: "knowledge-update",
		Question:     "What did the user say about adopting a dog?",
		Answer:       "The user never mentioned adopting a dog.",
	}, "The available history does not mention adopting a dog.")
	if err != nil {
		t.Fatalf("build abstention prompt: %v", err)
	}
	if !strings.Contains(prompt, "unanswerable") {
		t.Fatalf("expected abstention guidance in prompt, got: %s", prompt)
	}
}

func TestParseLongMemEvalJudgeLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    bool
		wantErr bool
	}{
		{name: "yes exact", input: "yes", want: true},
		{name: "yes with punctuation", input: "Yes.", want: true},
		{name: "no exact", input: "no", want: false},
		{name: "no with suffix", input: "No, the response is incomplete.", want: false},
		{name: "yesterday should fail", input: "yesterday it mentioned Portland", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseLongMemEvalJudgeLabel(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got label %v", test.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse judge label: %v", err)
			}
			if got != test.want {
				t.Fatalf("expected %v, got %v", test.want, got)
			}
		})
	}
}

func TestRunLongMemEvalQAAutoEval(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-judge" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "gpt-4o-mini" {
			t.Fatalf("unexpected judge model: %s", body.Model)
		}
		if len(body.Messages) != 1 {
			t.Fatalf("expected one user message, got %d", len(body.Messages))
		}
		requests++

		answer := "yes"
		if strings.Contains(body.Messages[0].Content, "Cycling and watercolor painting") {
			answer = "no"
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content": answer,
				},
			}},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	hypothesisPath := filepath.Join(tmpDir, "hypotheses.jsonl")
	if err := os.WriteFile(hypothesisPath, []byte(strings.Join([]string{
		`{"question_id":"q001","hypothesis":"The user now lives in Portland."}`,
		`{"question_id":"q002","hypothesis":"The user returned to cycling only."}`,
		`{"question_id":"q003_abs","hypothesis":"The history does not mention adopting a dog."}`,
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write hypotheses: %v", err)
	}

	evaluation, err := RunLongMemEvalQAAutoEval(
		context.Background(),
		filepath.Join("testdata", "longmemeval_fixture.json"),
		hypothesisPath,
		LongMemEvalQAJudgeOptions{
			Model:   "gpt-4o-mini",
			BaseURL: server.URL + "/v1",
			APIKey:  "sk-judge",
		},
	)
	if err != nil {
		t.Fatalf("run LongMemEval QA auto-eval: %v", err)
	}
	if requests != 3 {
		t.Fatalf("expected 3 judge requests, got %d", requests)
	}
	if evaluation.Summary.EvaluatedCount != 3 {
		t.Fatalf("expected 3 evaluated questions, got %d", evaluation.Summary.EvaluatedCount)
	}
	if math.Abs(evaluation.Summary.OverallAccuracy-(2.0/3.0)) > 1e-9 {
		t.Fatalf("unexpected overall accuracy: %.4f", evaluation.Summary.OverallAccuracy)
	}
	if evaluation.Summary.AbstentionAccuracy != 1 {
		t.Fatalf("expected abstention accuracy 1, got %.4f", evaluation.Summary.AbstentionAccuracy)
	}

	outDir := filepath.Join(tmpDir, "artifacts")
	if err := WriteLongMemEvalQAArtifacts(evaluation, outDir); err != nil {
		t.Fatalf("write LongMemEval QA artifacts: %v", err)
	}
	for _, name := range []string{"qa_summary.json", "qa_results.jsonl", "qa_report.md"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("expected artifact %s: %v", name, err)
		}
	}
}
