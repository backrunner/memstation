package eval

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"memstation/apps/server/internal/service/memoryllm"
)

func TestPipelineSamplingIsRecordedAndSentWithoutReaderReference(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		messages := payload["messages"].([]any)
		var input map[string]any
		if err := json.Unmarshal([]byte(messages[1].(map[string]any)["content"].(string)), &input); err != nil || len(input) != 2 || input["question"] != "question" || input["memories"] != "source" {
			t.Error("reader received extra evaluation data", input, err)
		}
		_, _ = w.Write([]byte(`{"model":"fixture","choices":[{"message":{"role":"assistant","content":"{\"answer\":\"ok\"}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	t.Setenv("ANSWERER_MODEL", "fixture")
	t.Setenv("ANSWERER_BASE_URL", server.URL)
	t.Setenv("ANSWERER_API_KEY", "fixture")
	t.Setenv("ANSWERER_TEMPERATURE", "0")
	config := pipelineReplayRoleConfig(t, "ANSWERER")
	_, _, _, err := pipelineCompleteObserved(context.Background(), config, pipelineInferenceInstruction, map[string]any{"question": "question", "memories": "source"})
	if err != nil || payload["temperature"] != float64(0) || config.runInfo().Temperature == nil || *config.runInfo().Temperature != 0 {
		t.Fatal("zero sampling temperature lost", err, payload["temperature"])
	}
}

func TestPipelinePreservesPartialRawResponseOnBudgetFailure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["temperature"] != float64(0) {
			t.Error("retry lost sampling control", payload, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": `{"answer":"partial`}, "finish_reason": "length"}}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}})
	}))
	defer server.Close()
	zero := float64(0)
	config := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture", Temperature: &zero}
	raw, usage, model, err := pipelineCompleteObservedBudget(context.Background(), config, "Return JSON", map[string]any{"question": "fixture", "memories": "source"}, 1024)
	if !errors.Is(err, memoryllm.ErrOutputBudgetExhausted) || calls != 2 || string(raw) != `{"answer":"partial` || model != "fixture" || usage.TotalTokens != 10 {
		t.Fatal("failed model evidence or usage discarded", string(raw), usage, model, err, calls)
	}
}

func TestPlainReaderAcceptsNaturalTextWhileJudgeStillRequiresJSON(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		output := "A supported natural-language answer."
		if calls == 1 {
			if _, exists := payload["response_format"]; exists {
				t.Error("free-text reader forced through unsupported structured output")
			}
		} else {
			if payload["response_format"].(map[string]any)["type"] != "json_object" {
				t.Error("judge lost its structured contract")
			}
			output = `{"correct":true,"reason":"fixture"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": output}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	config := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}
	answer, _, _, err := pipelineReadObserved(context.Background(), config, pipelineInterpretationInstruction, map[string]any{"question": "fixture", "memories": "source"}, 4096)
	if err != nil || answer != "A supported natural-language answer." {
		t.Fatal("valid text answer lost", answer, err)
	}
	raw, _, _, err := pipelineCompleteObserved(context.Background(), config, pipelineJudgeInstruction, map[string]any{"question": "fixture", "reference_answer": "fixture", "candidate_answer": answer})
	var grade struct {
		Correct bool   `json:"correct"`
		Reason  string `json:"reason"`
	}
	if err != nil || memoryllm.Decode(raw, &grade) != nil || !grade.Correct || calls != 2 {
		t.Fatal("invalid judge handling", string(raw), err)
	}
}
