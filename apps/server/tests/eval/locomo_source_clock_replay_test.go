package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/memoryllm"
)

// Manual matched-reader ablation. Retrieval and extraction are frozen; only
// explicit source session dates are carried into the bounded evidence packet.
// No reference answer or gold evidence is used to choose or enrich documents.
func TestLiveLOCOMOSourceClockReplay(t *testing.T) {
	out := os.Getenv("MEMSTATION_EVAL_CLOCK_REPLAY_OUT")
	if out == "" {
		t.Skip("manual frozen source-clock ablation")
	}
	revision := os.Getenv("MEMSTATION_EVAL_CODE_REVISION")
	if revision == "" {
		t.Fatal("code fingerprint required")
	}
	raw, err := os.ReadFile(os.Getenv("MEMSTATION_EVAL_CLOCK_REPLAY_PARENT"))
	if err != nil {
		t.Fatal(err)
	}
	var parent LOCOMOPipelineReport
	if err = json.Unmarshal(raw, &parent); err != nil || parent.CompletedSessions != parent.ExpectedSessions || len(parent.Answers) == 0 {
		t.Fatal("incomplete parent", err)
	}
	if err = os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	db, err := OpenEvaluationDatabase(ctx, os.Getenv("MEMSTATION_EVAL_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conversations := conversationstore.NewPostgresConversationRepository(db)
	engine := memoryservice.NewEngine(memorystore.NewInMemoryMemoryRepository(), nil)
	cfg := func(role string) LongMemEvalQAJudgeOptions {
		v, e := normalizeLongMemEvalQAJudgeOptions(LongMemEvalQAJudgeOptions{Model: os.Getenv(role + "_MODEL"), BaseURL: os.Getenv(role + "_BASE_URL"), APIKey: os.Getenv(role + "_API_KEY"), ReasoningEffort: os.Getenv(role + "_REASONING_EFFORT"), Timeout: 3 * time.Minute})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	reader, judge := cfg("ANSWERER"), cfg("JUDGE")
	budget := int(parent.Manifest["context_byte_budget"].(float64))
	manifest := map[string]any{"scope": "frozen retrieved candidates; source-clock metadata only; developmental paired QA", "parent_sha256": pipelineHash(string(raw)), "code_revision": revision, "context_bytes": budget, "reader": reader.runInfo(), "judge": judge.runInfo(), "judge_policy": "alternatives_v3", "retrieval_and_extraction_costs_historical": true, "source_clock": "explicit conversation metadata at matching write revision; no event date inference"}
	armNames := []string{"control", "source_clock"}
	if os.Getenv("MEMSTATION_EVAL_CLOCK_REPLAY_INFERENCE_ABLATION") == "true" {
		armNames = []string{"source_clock", "source_clock_inference"}
		manifest["scope"] = "same source-clock context, fresh coverage vs qualified-inference reader; developmental ablation"
	}
	manifest["coverage_prompt_sha256"] = pipelineHash(pipelineCoverageInstruction)
	manifest["inference_prompt_sha256"] = pipelineHash(pipelineInferenceInstruction)
	arms := map[string][]PipelineAnswer{}
	for _, arm := range armNames {
		arms[arm] = make([]PipelineAnswer, len(parent.Answers))
		for i, a := range parent.Answers {
			arms[arm][i] = PipelineAnswer{QuestionID: a.QuestionID, SampleID: a.SampleID, Mode: a.Mode, Error: "not executed"}
		}
	}
	save := func() {
		t.Helper()
		summaries := map[string]map[string]PipelineModeSummary{}
		for k, v := range arms {
			summaries[k] = summarizePipelineAnswers(v)
		}
		if e := writePipelineJSON(filepath.Join(out, "report.json"), map[string]any{"manifest": manifest, "arms": arms, "summaries": summaries}); e != nil {
			t.Fatal(e)
		}
	}
	save()
	for i, original := range parent.Answers {
		if original.Error != "" || !original.Judged {
			t.Fatal("parent has failed answer")
		}
		var frozen struct {
			Result struct {
				Memories []domain.MemoryDocument `json:"memories"`
			} `json:"result"`
		}
		encoded, e := json.Marshal(original.Retrieval)
		if e == nil {
			e = json.Unmarshal(encoded, &frozen)
		}
		if e != nil {
			t.Fatal(e)
		}
		docs := frozen.Result.Memories
		if len(docs) == 0 {
			t.Fatal("missing frozen retrieval")
		}
		user := docs[0].UserID
		scoped, e := security.WithPersonalDataScope(ctx, user, "")
		if e != nil {
			t.Fatal(e)
		}
		control, e := engine.PrepareEvidence(scoped, domain.SearchInput{UserID: user}, docs, memoryservice.EvidenceOptions{MaxBytes: budget})
		if e != nil || control.Text != original.Context {
			t.Fatal("control context changed", original.QuestionID, original.Mode, e)
		}
		enriched := append([]domain.MemoryDocument{}, docs...)
		for j, d := range enriched {
			if _, ok := d.Metadata["session_date"]; ok {
				continue
			} // Raw already retains its source clock.
			if d.ConversationID == "" {
				continue
			}
			c, e := conversations.GetConversation(scoped, user, d.ConversationID)
			if e != nil {
				t.Fatal(e)
			}
			if int64(intValue(d.Metadata["source_conversation_revision"])) != c.Revision {
				t.Fatal("source revision changed")
			}
			date, _ := c.Metadata["source_session_time"].(string)
			if date != "" && len(date) <= 240 {
				enriched[j].Metadata = maps.Clone(d.Metadata)
				enriched[j].Metadata["source_session_time"] = date
			}
		}
		clock, e := engine.PrepareEvidence(scoped, domain.SearchInput{UserID: user}, enriched, memoryservice.EvidenceOptions{MaxBytes: budget})
		if e != nil {
			t.Fatal(e)
		}
		for _, arm := range armNames {
			a := PipelineAnswer{SampleID: original.SampleID, QuestionID: original.QuestionID, Mode: original.Mode, Category: original.Category, Query: original.Query, Expected: original.Expected, Error: "attempt interrupted"}
			packet := control
			if arm != "control" {
				packet = clock
			}
			a.Context = packet.Text
			a.ContextBytes = len(packet.Text)
			a.ContextHash = pipelineHash(a.Context)
			a.ContextDocuments = len(packet.Memories)
			arms[arm][i] = a
			save()
			start := time.Now()
			readerPolicy := "coverage"
			if arm == "source_clock_inference" {
				readerPolicy = "coverage_inference"
			}
			answerText, usage, model, e := pipelineReadObserved(ctx, reader, pipelineReaderInstruction(readerPolicy), map[string]any{"question": a.Query, "memories": a.Context}, 4096)
			answerRaw := json.RawMessage(answerText)
			a.AnswerMillis = float64(time.Since(start).Milliseconds())
			a.AnswerUsage = usage
			a.AnswerModel = model
			a.Answer = answerText
			var judgeRaw json.RawMessage
			if e == nil && a.Answer != "" {
				start = time.Now()
				judgeRaw, a.JudgeUsage, a.JudgeModel, e = pipelineCompleteObserved(ctx, judge, pipelineAlternativeJudgeInstruction, map[string]any{"question": a.Query, "reference_answer": a.Expected, "candidate_answer": a.Answer})
				a.JudgeMillis = float64(time.Since(start).Milliseconds())
				var verdict struct {
					Correct *bool  `json:"correct"`
					Reason  string `json:"reason"`
				}
				if e == nil {
					e = memoryllm.Decode(judgeRaw, &verdict)
				}
				if e == nil && (verdict.Correct == nil || verdict.Reason == "") {
					e = fmt.Errorf("invalid judge schema")
				}
				if e == nil {
					a.Judged = true
					a.Correct = *verdict.Correct
					a.JudgeReason = verdict.Reason
				}
			}
			a.Error = ""
			if e != nil {
				a.Error = e.Error()
			} else if a.Answer == "" {
				a.Error = "empty answer"
			}
			arms[arm][i] = a
			if e := writePipelineAnswerObservation(filepath.Join(out, arm, a.QuestionID+"-"+a.Mode+".json"), a, answerRaw, judgeRaw); e != nil {
				t.Fatal(e)
			}
			save()
		}
	}
}

func writePipelineAnswerObservation(path string, answer PipelineAnswer, readerRaw, judgeRaw []byte) error {
	// Invalid model JSON is still diagnostic evidence, not valid embedded JSON.
	return writePipelineJSON(path, map[string]any{"answer": answer, "reader_raw": string(readerRaw), "judge_raw": string(judgeRaw)})
}

func TestInvalidModelReplyObservationDoesNotAbortFailureAccounting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observation.json")
	if err := writePipelineAnswerObservation(path, PipelineAnswer{QuestionID: "q", Error: "output budget exhausted"}, []byte("partial answer"), nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Answer    PipelineAnswer `json:"answer"`
		ReaderRaw string         `json:"reader_raw"`
	}
	if err = json.Unmarshal(raw, &saved); err != nil || saved.ReaderRaw != "partial answer" || saved.Answer.Error != "output budget exhausted" {
		t.Fatal("invalid reply or failed row lost", err, saved)
	}
}
