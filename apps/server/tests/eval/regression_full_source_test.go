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
)

// This uses every selected LOCOMO question and its complete original transcript,
// not an answer-directed oracle window. It diagnoses reader/input limitations;
// it is not a memory retrieval result or a proven theoretical ceiling.
func TestLiveRegressionFullSourceRead(t *testing.T) {
	out := os.Getenv("MEMSTATION_EVAL_REGRESSION_FULL_SOURCE_OUT")
	if out == "" {
		t.Skip("manual complete-original LOCOMO reader diagnostic")
	}
	if os.Getenv("MEMSTATION_EVAL_CODE_REVISION") == "" {
		t.Fatal("pinned code revision required")
	}
	raw, err := os.ReadFile(os.Getenv("MEMSTATION_EVAL_REGRESSION_PLAN"))
	if err != nil {
		t.Fatal(err)
	}
	var plan regressionPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	groups, err := loadRegressionGroups(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(out, 0o700); err != nil {
		t.Fatal("fresh private output required", err)
	}
	mode := "full_original"
	var observations map[string]map[string]string
	policy := os.Getenv("MEMSTATION_EVAL_REGRESSION_PROVIDED_IMAGES")
	if policy != "" && policy != "true" {
		t.Fatal("provided-image policy must be explicit true or unset")
	}
	if policy == "true" {
		mode = "full_original_with_image_observations"
		observations, err = regressionProvidedImageObservations(plan.Locomo.Path)
		if err != nil {
			t.Fatal(err)
		}
	}
	reader := regressionRole(t, "ANSWERER", plan.MaxTokens, plan.ReaderThinking["locomo"])
	judge := regressionRole(t, "JUDGE", plan.MaxTokens, "")
	contexts := map[string]string{}
	hashes := map[string]string{}
	expected := 0
	for _, g := range groups {
		if g.Locomo == nil {
			continue
		}
		var text strings.Builder
		for _, s := range g.Sessions {
			fmt.Fprintf(&text, "[Conversation date: %s]\n", s.Date)
			for _, message := range s.Messages {
				text.WriteString(strings.TrimPrefix(message.Text, fmt.Sprintf("[Conversation date: %s] ", s.Date)))
				if ref, ok := message.Metadata["ref"].(string); ok {
					if observation := observations[g.Locomo.ID][ref]; observation != "" {
						text.WriteByte(' ')
						text.WriteString(observation)
					}
				}
				text.WriteByte('\n')
			}
		}
		contexts[g.ID] = text.String()
		hashes[g.ID] = pipelineHash(text.String())
		expected += len(g.Questions)
		for _, q := range g.Questions {
			row := regressionOutcome{Dataset: g.Dataset, Group: g.ID, QuestionID: q.ID, Mode: mode, PriorStatus: q.Prior, Answer: externalAnswer{Question: q.Query, Reference: q.Reference, Error: "not executed"}}
			if err = writePipelineJSON(regressionOutcomePath(out, g, q, mode), row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if expected == 0 {
		t.Fatal("full-source diagnostic requires LOCOMO cases")
	}
	if err = writePipelineJSON(filepath.Join(out, "manifest.json"), map[string]any{"plan_sha256": pipelineHash(string(raw)), "code_revision": os.Getenv("MEMSTATION_EVAL_CODE_REVISION"), "expected_answers": expected, "source_context_sha256": hashes, "provided_image_observations": policy == "true", "max_context_bytes": plan.ContextBytes, "reader": reader.runInfo(), "judge": judge.runInfo(), "reader_prompt_sha256": pipelineHash(pipelineInterpretationInstruction), "scope": "All fixed LOCOMO questions; every original source turn with date/speaker. An explicit separately reported track may also include the provided BLIP image caption and image search query as labelled image metadata. No event summaries, generated memories, QA references, evidence labels or query-directed source filtering. Same reader/prompt/output budget; no retrieval/worker output. Oversize complete sources fail explicitly without truncation. Not a retrieval score, held-out result or proven theoretical upper bound."}); err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Locomo == nil {
			continue
		}
		for _, q := range g.Questions {
			row := regressionOutcome{Dataset: g.Dataset, Group: g.ID, QuestionID: q.ID, Mode: mode, PriorStatus: q.Prior, Answer: externalAnswer{Question: q.Query, Reference: q.Reference, Context: contexts[g.ID], ContextHash: hashes[g.ID], Error: "attempt interrupted before completion"}}
			path := regressionOutcomePath(out, g, q, mode)
			if err = writePipelineJSON(path, row); err != nil {
				t.Fatal(err)
			}
			row.Answer.Error = ""
			if len(row.Answer.Context) > plan.ContextBytes {
				row.Answer.Error = "complete source exceeds the shared input budget; not truncated"
			} else {
				regressionLocomoRead(context.Background(), &row.Answer, reader, judge, map[string]any{"question": q.Query, "question_date": q.Date, "memories": row.Answer.Context}, plan.ReaderTokens)
			}
			if err = writePipelineJSON(path, row); err != nil {
				t.Fatal(err)
			}
			t.Logf("full original %s bytes=%d correct=%t error=%s", q.ID, len(row.Answer.Context), row.Answer.Correct, row.Answer.Error)
		}
	}
	if err = writePipelineJSON(filepath.Join(out, "completion.json"), map[string]any{"completed_at": time.Now().UTC(), "expected_answers": expected}); err != nil {
		t.Fatal(err)
	}
}

// Image descriptions and the image retrieval query belong to the conversation's
// supplied multimodal input. They are distinct from QA/event/memory annotations.
// This diagnostic never fetches a gold answer or treats a search query as a
// verified personal assertion. The primary human-only corpus stays unchanged.
func regressionProvidedImageObservations(path string) (map[string]map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var samples []struct {
		ID           string                     `json:"sample_id"`
		Conversation map[string]json.RawMessage `json:"conversation"`
	}
	if err = json.Unmarshal(raw, &samples); err != nil {
		return nil, err
	}
	result := map[string]map[string]string{}
	for _, sample := range samples {
		result[sample.ID] = map[string]string{}
		for key, value := range sample.Conversation {
			if !isLOCOMOSessionKey(key) {
				continue
			}
			var turns []struct {
				ID      string `json:"dia_id"`
				Caption string `json:"blip_caption"`
				Query   string `json:"query"`
			}
			if err = json.Unmarshal(value, &turns); err != nil {
				return nil, err
			}
			for _, turn := range turns {
				caption, query := strings.TrimSpace(turn.Caption), strings.TrimSpace(turn.Query)
				if caption == "" && query == "" {
					continue
				}
				observation := map[string]string{}
				if caption != "" {
					observation["provided_blip_caption"] = caption
				}
				if query != "" {
					observation["image_retrieval_query"] = query
				}
				text, err := json.Marshal(observation)
				if err != nil {
					return nil, err
				}
				result[sample.ID][turn.ID] = "[Supplied image metadata, generated observation rather than a human assertion: " + string(text) + "]"
			}
		}
	}
	return result, nil
}

func TestRegressionProvidedImagesExcludeQAAndEventAnnotations(t *testing.T) {
	path := pipelineFixture(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var samples []map[string]any
	if err = json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	conversation := samples[0]["conversation"].(map[string]any)
	turn := conversation["session_1"].([]any)[0].(map[string]any)
	turn["blip_caption"] = "A dog and sunflowers on a tattoo"
	turn["query"] = "dog tattoo"
	raw, err = json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	observations, err := regressionProvidedImageObservations(path)
	if err != nil {
		t.Fatal(err)
	}
	text := observations["synthetic"]["D1:1"]
	if !strings.Contains(text, "sunflowers") || !strings.Contains(text, "dog tattoo") || strings.Contains(fmt.Sprint(observations), "GOLD_SENTINEL") {
		t.Fatal("image observations lost provenance or included annotations", observations)
	}
}
