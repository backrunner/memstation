package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"memstation/apps/server/internal/service/memoryllm"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

func TestCoreReplayReaderCannotSeeReferenceAndFailedJudgeStaysInDenominator(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
			t.Error("invalid request")
			w.WriteHeader(400)
			return
		}
		calls.Add(1)
		prompt, data := request.Messages[0].Content, request.Messages[1].Content
		output := "supported candidate"
		switch prompt {
		case pipelineCoverageInstruction:
			if strings.Contains(data, "GOLD_SENTINEL") || strings.Contains(data, "GOLD_REF") || !strings.Contains(data, "frozen evidence") {
				t.Error("reader did not receive only question and frozen context")
			}
		case pipelineAlternativeJudgeInstruction:
			if !strings.Contains(data, "GOLD_SENTINEL") || !strings.Contains(data, "supported candidate") {
				t.Error("judge missing required inputs")
			}
			output = `{"reason":"malformed verdict"}`
		default:
			t.Error("unexpected model task")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "choices": []any{map[string]any{"message": map[string]string{"content": output}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
	a := PipelineAnswer{Mode: "raw", Query: "question", Expected: "GOLD_SENTINEL", EvidenceRefs: []string{"GOLD_REF"}, Context: "frozen evidence"}
	answerFrozenEvidence(context.Background(), &a, cfg, cfg)
	if calls.Load() != 2 || a.Judged || a.Error != "invalid judge schema" || a.Answer != "supported candidate" {
		t.Fatal(a, calls.Load())
	}
	r := summarizePipelineAnswers([]PipelineAnswer{a})["raw"]
	if r.Expected != 1 || r.Errors != 1 || r.Judged != 0 {
		t.Fatal("failed verdict removed from denominator", r)
	}
}

func TestPlainContextCoreReportsWholeRecordOmission(t *testing.T) {
	docs := []domain.MemoryDocument{{ID: "core", Type: domain.MemoryTypeFact, Content: "not an approved plan"}, {ID: "small", Type: domain.MemoryTypeFact, Content: "yes"}}
	text, selected := packPipelineContext(docs, len("[fact] not an approved plan\n")-1)
	coverage := memoryservice.MeasureEvidenceCoverage([]string{"core"}, selected)
	if len(coverage.OmittedIDs) != 1 || coverage.OmittedIDs[0] != "core" || strings.Contains(text, "approved") || len(selected) != 1 || selected[0].ID != "small" {
		t.Fatal("truncated or silently lost protected record", text, coverage)
	}
}

func TestCoreReplayInvalidReaderClearsHistoricalVerdict(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "new-reader", "choices": []any{map[string]any{"message": map[string]string{"content": " "}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
	a := PipelineAnswer{Mode: "raw", Query: "question", Context: "evidence", Answer: "old", Judged: true, Correct: true, JudgeReason: "old", JudgeModel: "old-judge", JudgeMillis: 123, JudgeUsage: domain.UsageRecord{TotalTokens: 100}}
	answerFrozenEvidence(context.Background(), &a, cfg, cfg)
	if calls.Load() != 1 || a.Judged || a.Correct || a.JudgeReason != "" || a.JudgeModel != "" || a.JudgeMillis != 0 || a.JudgeUsage.TotalTokens != 0 || a.Answer != "" || a.Error != "reader failed" {
		t.Fatal("reader failure inherited historical judgment", a, calls.Load())
	}
}

func answerFrozenEvidence(ctx context.Context, a *PipelineAnswer, reader, judge LongMemEvalQAJudgeOptions) {
	// A reader failure must not inherit a previous verdict/model from a frozen row.
	a.Answer, a.JudgeReason, a.JudgeModel = "", "", ""
	a.Correct, a.Judged = false, false
	a.JudgeUsage, a.JudgeMillis = domain.UsageRecord{}, 0
	start := time.Now()
	answer, usage, model, err := pipelineReadObserved(ctx, reader, pipelineCoverageInstruction, map[string]any{"question": a.Query, "memories": a.Context}, 4096)
	a.AnswerUsage, a.AnswerModel, a.AnswerMillis = usage, model, float64(time.Since(start).Microseconds())/1000
	if err != nil {
		a.Error = "reader failed"
		return
	}
	a.Answer = answer
	start = time.Now()
	raw, usage, model, err := pipelineCompleteObserved(ctx, judge, pipelineAlternativeJudgeInstruction, map[string]any{"question": a.Query, "reference_answer": a.Expected, "candidate_answer": a.Answer})
	a.JudgeUsage, a.JudgeModel, a.JudgeMillis = usage, model, float64(time.Since(start).Microseconds())/1000
	var grade struct {
		Correct *bool  `json:"correct"`
		Reason  string `json:"reason"`
	}
	if err != nil {
		a.Error = "judge failed"
		return
	}
	if memoryllm.Decode(raw, &grade) != nil || grade.Correct == nil || grade.Reason == "" {
		a.Error = "invalid judge schema"
		return
	}
	a.Correct, a.Judged, a.JudgeReason, a.Error = *grade.Correct, true, grade.Reason, ""
}
