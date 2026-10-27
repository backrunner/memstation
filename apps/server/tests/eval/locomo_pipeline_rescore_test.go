package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestLOCOMORescorePreservesAnswersAndFailureDenominator(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 2 || req.Messages[0].Content != pipelineJudgeInstruction {
			t.Error("rescore called a non-judge")
		}
		output := `{"correct":true,"reason":"equivalent"}`
		if calls.Add(1) == 3 {
			output = `{"correct":null,"reason":"bad schema"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": output}, "finish_reason": "stop"}}, "usage": map[string]int{"total_tokens": 20}})
	}))
	defer server.Close()
	dir := t.TempDir()
	source := filepath.Join(dir, "parent.json")
	output := filepath.Join(dir, "rescore.json")
	parent := LOCOMOPipelineReport{ExpectedSessions: 1, CompletedSessions: 1, Manifest: map[string]any{"code_revision": "ingestion-revision"}}
	for _, mode := range []string{"raw", "derived", "combined"} {
		parent.Answers = append(parent.Answers, PipelineAnswer{QuestionID: "q", Mode: mode, Query: "question", Expected: "reference", Answer: "original answer", Judged: true, Correct: false, Context: "original context", AnswerUsage: domain.UsageRecord{TotalTokens: 123}})
	}
	if e := writePipelineJSON(source, parent); e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(source)
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
	got, e := RescoreLOCOMOPipeline(context.Background(), source, output, "judge-revision", cfg)
	if e == nil || calls.Load() != 3 {
		t.Fatal(e, calls.Load())
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(original, after) {
		t.Fatal("parent modified")
	}
	if got.Manifest["code_revision"] != "ingestion-revision" || got.Manifest["parent_report_sha256"] != pipelineHash(string(original)) {
		t.Fatal("lost source provenance")
	}
	for _, a := range got.Answers {
		if a.Answer != "original answer" || a.Context != "original context" || a.AnswerUsage.TotalTokens != 123 {
			t.Fatal("rewrote generated answer")
		}
	}
	if got.Modes["combined"].Expected != 1 || got.Modes["combined"].Errors != 1 || got.Modes["combined"].Judged != 0 || got.Modes["raw"].Correct != 1 {
		t.Fatal(got.Modes)
	}
	if _, e = RescoreLOCOMOPipeline(context.Background(), source, output, "again", cfg); e == nil {
		t.Fatal("overwrote earlier rescore")
	}
	if _, e = RescoreLOCOMOPipeline(context.Background(), source, source, "again", cfg); e == nil {
		t.Fatal("overwrote original")
	}
}

func TestAlternativeJudgePolicyIsExplicitAndPreservesOriginal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 2 || req.Messages[0].Content != pipelineAlternativeJudgeInstruction {
			t.Error("wrong explicit judge policy")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"correct":true,"reason":"one alternative is sufficient"}`}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	dir := t.TempDir()
	source := filepath.Join(dir, "parent.json")
	out := filepath.Join(dir, "v3.json")
	parent := LOCOMOPipelineReport{Answers: []PipelineAnswer{{QuestionID: "q", Mode: "raw", Query: "one possible explanation?", Expected: "option A, option B", Answer: "option A", Judged: true, Context: "frozen"}}}
	if err := writePipelineJSON(source, parent); err != nil {
		t.Fatal(err)
	}
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
	if _, err := RescoreLOCOMOPipelineWithPolicy(context.Background(), source, out, "test", cfg, "invalid"); err == nil {
		t.Fatal("unknown policy accepted")
	}
	got, err := RescoreLOCOMOPipelineWithPolicy(context.Background(), source, out, "test", cfg, "alternatives_v3")
	if err != nil || got.Manifest["rescore_judge_policy"] != "alternatives_v3" || got.Manifest["judge_prompt_sha256"] != pipelineHash(pipelineAlternativeJudgeInstruction) || got.Answers[0].Answer != "option A" || got.Answers[0].Context != "frozen" {
		t.Fatal(got.Manifest, err)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var original LOCOMOPipelineReport
	if err = json.Unmarshal(raw, &original); err != nil || original.Answers[0].Correct {
		t.Fatal("overwrote parent grade")
	}
}
