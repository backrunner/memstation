package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"memstation/apps/server/internal/domain"
)

// scriptedChat replies with the given contents in order and counts calls.
func scriptedChat(t *testing.T, replies ...string) (LongMemEvalQAJudgeOptions, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		n := int(calls.Add(1))
		if n > len(replies) {
			t.Error("unexpected extra model call")
			n = len(replies)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": replies[n-1]}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
	}))
	t.Cleanup(server.Close)
	return LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "fixture"}, &calls
}

func TestReaderFormatRetryOnlyAfterUnusableReply(t *testing.T) {
	payload := map[string]any{"question": "q", "memories": "m"}
	instruction := pipelineReaderInstruction("inclusive_reasoning")

	cfg, calls := scriptedChat(t, "work\nFINAL ANSWER: Toronto")
	_, answer, usage, _, attempts, err := pipelineReadFinal(context.Background(), cfg, "inclusive_reasoning", instruction, payload, 1024, nil)
	if err != nil || answer != "Toronto" || attempts != 1 || calls.Load() != 1 || usage.TotalTokens != 15 {
		t.Fatal("valid reply must not be retried", answer, attempts, calls.Load(), err)
	}

	cfg, calls = scriptedChat(t, "worked it out, no marker", "work\nFINAL ANSWER: Toronto")
	_, answer, usage, _, attempts, err = pipelineReadFinal(context.Background(), cfg, "inclusive_reasoning", instruction, payload, 1024, nil)
	if err != nil || answer != "Toronto" || attempts != 2 || calls.Load() != 2 || usage.TotalTokens != 30 || usage.InputTokens+usage.OutputTokens != usage.TotalTokens {
		t.Fatal("missing final line gets one retry with both usages counted", answer, attempts, usage, err)
	}

	cfg, calls = scriptedChat(t, "no marker", "still no marker")
	_, _, _, _, attempts, err = pipelineReadFinal(context.Background(), cfg, "inclusive_reasoning", instruction, payload, 1024, nil)
	if err == nil || attempts != 2 || calls.Load() != 2 {
		t.Fatal("a second unusable reply stays a failure", attempts, err)
	}

	cfg, calls = scriptedChat(t, "no marker")
	_, _, _, _, attempts, err = pipelineReadFinal(context.Background(), cfg, "inclusive_reasoning", instruction, payload, 1024, func() bool { return false })
	if err == nil || attempts != 1 || calls.Load() != 1 {
		t.Fatal("a refused budget reservation must prevent the retry", attempts, err)
	}

	cfg, calls = scriptedChat(t, "plain reply without any marker")
	_, answer, _, _, attempts, err = pipelineReadFinal(context.Background(), cfg, "interpretation", pipelineReaderInstruction("interpretation"), payload, 1024, nil)
	if err != nil || answer != "plain reply without any marker" || attempts != 1 || calls.Load() != 1 {
		t.Fatal("policies without a final line are never retried", answer, attempts, err)
	}
}

func TestJudgeFormatRetryOnlyAfterInvalidVerdict(t *testing.T) {
	payload := map[string]any{"question": "q", "reference_answer": "r", "candidate_answer": "c"}

	cfg, calls := scriptedChat(t, `{"correct":false,"reason":"different"}`)
	_, _, _, attempts, correct, _, err := pipelineJudgeFinal(context.Background(), cfg, pipelineJudgeInstruction, payload, 4096, nil)
	if err != nil || correct || attempts != 1 || calls.Load() != 1 {
		t.Fatal("a valid verdict, including false, must not be retried", attempts, err)
	}

	cfg, calls = scriptedChat(t, "", `{"correct":true,"reason":"same"}`)
	_, usage, _, attempts, correct, reason, err := pipelineJudgeFinal(context.Background(), cfg, pipelineJudgeInstruction, payload, 4096, nil)
	if err != nil || !correct || reason != "same" || attempts != 2 || calls.Load() != 2 || usage.TotalTokens != 30 {
		t.Fatal("an empty verdict gets one retry", attempts, usage, err)
	}

	cfg, calls = scriptedChat(t, `{"correct":null,"reason":"x"}`, `{"correct":null,"reason":"x"}`)
	_, _, _, attempts, _, _, err = pipelineJudgeFinal(context.Background(), cfg, pipelineJudgeInstruction, payload, 4096, nil)
	if err == nil || attempts != 2 || calls.Load() != 2 {
		t.Fatal("a second invalid verdict stays a failure", attempts, err)
	}
}

func TestInclusiveReaderIsARegisteredFinalLinePolicy(t *testing.T) {
	inclusive := pipelineReaderInstruction("inclusive_reasoning")
	if inclusive == pipelineReaderInstruction("inference_reasoning") || inclusive == pipelineAnswerInstruction || !strings.Contains(inclusive, pipelineFinalAnswerMarker) {
		t.Fatal("inclusive policy must have its own final-line instruction")
	}
	if _, err := pipelineFinalAnswer("inclusive_reasoning", "no marker"); err == nil {
		t.Fatal("inclusive replies need a final line")
	}
	o := LOCOMOPipelineOptions{ReaderPolicy: "inclusive_reasoning"}
	if err := normalizePipelineProtocol(&o); err != nil && strings.Contains(err.Error(), "reader policy") {
		t.Fatal("inclusive policy rejected by the protocol", err)
	}
	plan := packetReplayPlan{ReaderPolicy: "inclusive_reasoning", ContextBytes: 32000, ReaderOutputTokens: 16384, JudgeOutputTokens: 4096, MaxGenerationCalls: 4, TokenStopLimit: 1, Cases: []packetReplayCase{{Group: "control", Baseline: PipelineAnswer{QuestionID: "q", Context: "c", ContextHash: pipelineHash("c"), ContextBytes: 1, ContextDocuments: 1}, Documents: []domain.MemoryDocument{{ID: "record"}}}}}
	if err := validatePacketReplayPlan(plan); err != nil {
		t.Fatal("inclusive policy rejected by packet replay", err)
	}
}

func TestCandidateGateTreatsFormatRetryAsProtocol(t *testing.T) {
	base := map[string]any{"version": float64(4), "answer_prompt_sha256": strings.Repeat("a", 64)}
	for _, key := range []string{"dataset_sha256", "answerer", "judge", "embedding", "processing", "rerank", "top_n", "reader_policy", "reader_format", "context_format", "context_byte_budget", "source_neighbor_turns", "qa_output_tokens", "judge_initial_output_tokens", "judge_prompt_sha256", "refine_evidence", "image_policy", "judge_protocol", "embedding_revision"} {
		base[key] = "x"
	}
	old, err := candidateProtocol(base)
	if err != nil || old["format_retry"] != "none" {
		t.Fatal("reports before the retry policy must read as no retry", err, old["format_retry"])
	}
	withRetry := map[string]any{}
	for k, v := range base {
		withRetry[k] = v
	}
	withRetry["format_retry"] = pipelineFormatRetryPolicy
	current, err := candidateProtocol(withRetry)
	if err != nil || current["format_retry"] == old["format_retry"] {
		t.Fatal("a retry policy change must be visible to the gate", err)
	}
}

func TestExternalLongMemReaderPolicies(t *testing.T) {
	base, current := externalLongMemReaderInstruction("interpretation"), externalLongMemReaderInstruction("current_state_interpretation")
	if base != pipelineInterpretationInstruction || current == "" || current == base || !strings.HasPrefix(current, base) {
		t.Fatal("current-state reader must extend the interpretation reader")
	}
	for _, policy := range []string{"", "inference_reasoning", "baseline", "unknown"} {
		if externalLongMemReaderInstruction(policy) != "" {
			t.Fatal("external runner accepted an unsupported reader policy", policy)
		}
	}
	o := LOCOMOPipelineOptions{ReaderPolicy: "current_state_interpretation"}
	if err := normalizePipelineProtocol(&o); err != nil && strings.Contains(err.Error(), "reader policy") {
		t.Fatal(err)
	}
}
