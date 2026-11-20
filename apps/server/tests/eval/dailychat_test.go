package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"memstation/apps/server/internal/domain"
)

const dailyChatDevPath = "testdata/daily_chat/daily_chat_dev_v1.json"

func TestDailyChatDevDatasetContract(t *testing.T) {
	samples, err := loadDailyChatSamples(dailyChatDevPath, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	languages := map[string]int{}
	skills := map[string]int{}
	for _, s := range samples {
		axes := map[string]int{}
		cross := 0
		for _, q := range s.Questions {
			total++
			axes[q.Axis]++
			languages[q.Language]++
			for _, skill := range q.Skills {
				skills[skill]++
			}
			if slices.Contains(q.Skills, "cross_lingual") {
				cross++
			}
			if q.AskedAt == "" || !strings.HasPrefix(q.ID, s.ID+".") {
				t.Fatalf("question %s lacks time or sample-scoped identity", q.ID)
			}
		}
		// Every sample must exercise every axis, at least two abstentions and one
		// cross-lingual question, so no history only tests a single memory skill.
		for _, axis := range dailyChatAxes {
			if axes[axis] == 0 {
				t.Fatalf("%s has no %s questions", s.ID, axis)
			}
		}
		if axes["abstention"] < 2 || cross == 0 || len(s.Sessions) < 6 {
			t.Fatalf("%s is too narrow: %v cross=%d sessions=%d", s.ID, axes, cross, len(s.Sessions))
		}
	}
	if total != 218 || len(samples) != 8 {
		t.Fatalf("development split changed size without updating its contract: %d questions", total)
	}
	for _, language := range []string{"zh", "en", "ja", "ko"} {
		if languages[language] < 20 {
			t.Fatalf("too few %s questions: %v", language, languages)
		}
	}
	for _, skill := range []string{"relative_time", "update", "alias", "attribution", "plan_vs_done", "assistant_not_evidence", "count", "near_miss", "code_switch"} {
		if skills[skill] == 0 {
			t.Fatalf("no question exercises %s", skill)
		}
	}
	// Assistant turns are ingested as assistant messages, not as user facts.
	for _, s := range samples {
		for _, session := range s.Sessions {
			for _, m := range session.Messages {
				if strings.Contains(m.Text, "[Speaker: Assistant]") != (m.Role == domain.RoleAssistant) {
					t.Fatalf("%s: assistant role mismatch in %q", s.ID, m.Text)
				}
			}
		}
	}
}

func TestDailyChatRejectsLeakyOrAmbiguousItems(t *testing.T) {
	base := func() dailyChatDataset {
		return dailyChatDataset{Schema: dailyChatSchema, Split: "development", Samples: []dailyChatSample{{
			ID: "s", Language: "en", Scene: "assistant_1on1", OwnerSpeaker: "Ana",
			Sessions: []dailyChatSession{
				{ID: "session_1", At: "2026-01-01T10:00:00Z", Turns: []dailyChatTurn{{ID: "D1:1", Role: "user", Speaker: "Ana", Text: "I adopted a cat."}, {ID: "D1:2", Role: "assistant", Speaker: "Assistant", Text: "Name it Miso!"}}},
				{ID: "session_2", At: "2026-01-02T10:00:00Z", Turns: []dailyChatTurn{{ID: "D2:1", Role: "user", Speaker: "Ana", Text: "Her name is Tofu."}}},
			},
			Questions: []dailyChatQuestion{{ID: "q1", AskedAt: "2026-01-03T10:00:00Z", AskedBy: "Ana", Language: "en", Axis: "fact", Skills: []string{"profile"}, Query: "What is my cat's name?", Answer: "Tofu", Evidence: []string{"D2:1"}}},
		}}}
	}
	if _, err := buildDailyChatSamples(base()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*dailyChatDataset){
		"assistant evidence":   func(d *dailyChatDataset) { d.Samples[0].Questions[0].Evidence = []string{"D1:2"} },
		"asked before history": func(d *dailyChatDataset) { d.Samples[0].Questions[0].AskedAt = "2026-01-01T12:00:00Z" },
		"unlabelled cross":     func(d *dailyChatDataset) { d.Samples[0].Questions[0].Language = "zh" },
		"labelled same-lang":   func(d *dailyChatDataset) { d.Samples[0].Questions[0].Skills = []string{"cross_lingual"} },
		"abstain with evidence": func(d *dailyChatDataset) {
			d.Samples[0].Questions[0].Axis = "abstention"
			d.Samples[0].Questions[0].Skills = []string{"unanswerable"}
		},
		"answerable no evidence": func(d *dailyChatDataset) { d.Samples[0].Questions[0].Evidence = nil },
		"unknown axis":           func(d *dailyChatDataset) { d.Samples[0].Questions[0].Axis = "vibes" },
		"unknown skill":          func(d *dailyChatDataset) { d.Samples[0].Questions[0].Skills = []string{"guess"} },
		"unresolved evidence":    func(d *dailyChatDataset) { d.Samples[0].Questions[0].Evidence = []string{"D9:9"} },
		"out of order":           func(d *dailyChatDataset) { d.Samples[0].Sessions[1].At = "2025-12-31T10:00:00Z" },
		"foreign asker":          func(d *dailyChatDataset) { d.Samples[0].Questions[0].AskedBy = "Bo" },
		"foreign 1:1 speaker":    func(d *dailyChatDataset) { d.Samples[0].Sessions[1].Turns[0].Speaker = "Bo" },
		"group assistant":        func(d *dailyChatDataset) { d.Samples[0].Scene = "group_chat" },
		"wrong schema":           func(d *dailyChatDataset) { d.Schema = "locomo" },
	}
	for name, mutate := range cases {
		d := base()
		mutate(&d)
		if _, err := buildDailyChatSamples(d); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := loadPipelineSamplesForFormat(PipelineDatasetDailyChat, dailyChatDevPath, "", 0, true); err == nil {
		t.Fatal("daily chat accepted a caption track")
	}
	if _, err := loadPipelineSamplesForFormat("other", dailyChatDevPath, "", 0, false); err == nil {
		t.Fatal("unknown dataset format accepted")
	}
}

func TestDailyChatSamplingStratifiesByAxis(t *testing.T) {
	samples, err := loadDailyChatSamples(dailyChatDevPath, "mixed-group-01", 2)
	if err != nil || len(samples) != 1 {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, q := range samples[0].Questions {
		counts[q.Axis]++
	}
	for _, axis := range dailyChatAxes {
		if counts[axis] != 2 {
			t.Fatalf("expected two %s questions, got %v", axis, counts)
		}
	}
	if len(samples[0].Sessions) != 7 {
		t.Fatal("sampling dropped source sessions")
	}
}

// The reader sees question time and asker; dataset labels and gold never reach a model.
func TestDailyChatControlRunPassesQuestionContextOnly(t *testing.T) {
	var mu sync.Mutex
	readerKeys := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 {
			w.WriteHeader(400)
			return
		}
		output := `{"answer":"unknown"}`
		if body.Messages[0].Content == pipelineJudgeInstruction {
			output = `{"correct":false,"reason":"fixture"}`
		} else {
			var payload map[string]any
			if err := json.Unmarshal([]byte(body.Messages[1].Content), &payload); err != nil {
				t.Error("reader payload is not JSON")
			}
			mu.Lock()
			for key := range payload {
				readerKeys[key] = true
			}
			mu.Unlock()
			if at, _ := payload["question_time"].(string); !strings.Contains(at, "Monday, 25 May 2026") {
				t.Errorf("missing rendered question time: %v", payload["question_time"])
			}
			for _, leak := range []string{"zh-assistant-01.", "D1:1", "\"axis\"", "relative_time", "cross_lingual", "2026年5月16日"} {
				if strings.Contains(body.Messages[1].Content, leak) {
					t.Errorf("reader received %q", leak)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": output}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
	}))
	defer server.Close()
	cfg := LongMemEvalQAJudgeOptions{Model: "fixture", BaseURL: server.URL, APIKey: "k"}
	path, err := filepath.Abs(dailyChatDevPath)
	if err != nil {
		t.Fatal(err)
	}
	o := LOCOMOPipelineOptions{DatasetPath: path, DatasetFormat: PipelineDatasetDailyChat, SampleID: "zh-assistant-01", QuestionsPerCategory: 1, OutputDir: t.TempDir(), Prefix: "eval-dailychat", CodeRevision: "fixture", TopN: 5, ContextBytes: 4096, OracleContextBytes: 64000, Modes: []string{"question_only", "full_history"}, Answerer: cfg, Judge: cfg}
	report, err := RunLOCOMOPipeline(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sortedKeys(readerKeys), []string{"asker", "memories", "question", "question_time"}) {
		t.Fatalf("unexpected reader payload keys %v", sortedKeys(readerKeys))
	}
	if len(report.Answers) != 10 || len(report.ByAxis) != 5 || report.ByLanguage["zh"]["full_history"].Expected == 0 || report.BySkill == nil {
		t.Fatalf("missing label summaries: %d %v", len(report.Answers), report.ByAxis)
	}
	if report.Manifest["dataset_format"] != PipelineDatasetDailyChat {
		t.Fatal("daily-chat manifest is not labelled")
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestDailyChatValidateSealed validates an external (for example sealed held-out)
// file and prints only its hash and label counts, never questions or turns, so a
// split can be checked without exposing its contents to whoever tunes the system.
func TestDailyChatValidateSealed(t *testing.T) {
	path := os.Getenv("MEMSTATION_EVAL_DAILY_CHAT_VALIDATE")
	if path == "" {
		t.Skip("set MEMSTATION_EVAL_DAILY_CHAT_VALIDATE to a daily-chat file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("unreadable dataset")
	}
	var dataset dailyChatDataset
	if err = json.Unmarshal(raw, &dataset); err != nil {
		t.Fatal("dataset is not valid JSON for the daily-chat schema")
	}
	samples, err := buildDailyChatSamples(dataset)
	if err != nil {
		// Validation errors name item identifiers and rules, not item text.
		t.Fatal(err)
	}
	counts := map[string]map[string]int{"language": {}, "axis": {}, "skill": {}, "scene": {}}
	total, sessions := 0, 0
	for _, s := range dataset.Samples {
		counts["scene"][s.Scene]++
	}
	for _, s := range samples {
		sessions += len(s.Sessions)
		for _, q := range s.Questions {
			total++
			counts["language"][q.Language]++
			counts["axis"][q.Axis]++
			for _, skill := range q.Skills {
				counts["skill"][skill]++
			}
		}
	}
	encoded, _ := json.Marshal(map[string]any{"sha256": pipelineHash(string(raw)), "split": dataset.Split, "samples": len(samples), "sessions": sessions, "questions": total, "counts": counts})
	fmt.Println(string(encoded))
}

// Version 2 lengthens every history with audited distractor sessions. Its questions are
// version 1's, changed only by the recorded reference revisions, and every history must
// be far larger than a 16KB reader context so retrieval has to choose.
func TestDailyChatDevV2LongHistoryContract(t *testing.T) {
	v1, err := loadDailyChatSamples(dailyChatDevPath, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	const v2Path = "testdata/daily_chat/daily_chat_dev_v2.json"
	v2, err := loadDailyChatSamples(v2Path, "", 0)
	if err != nil || len(v2) != len(v1) {
		t.Fatal("v2 must load with the same samples", err)
	}
	raw, err := os.ReadFile(v2Path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Provenance struct {
			Revisions []struct {
				Sample, Question, Field, Value, Reason string
			} `json:"reference_revisions"`
		} `json:"provenance"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil || len(doc.Provenance.Revisions) != 6 {
		t.Fatal("v2 must record its reference revisions", err)
	}
	revised := map[string]string{}
	for _, r := range doc.Provenance.Revisions {
		if r.Reason == "" {
			t.Fatal("unexplained revision")
		}
		revised[r.Sample+"."+r.Question+"/"+r.Field] = r.Value
	}
	for i, s := range v2 {
		bytes := 0
		for _, session := range s.Sessions {
			for _, m := range session.Messages {
				bytes += len(m.Text)
			}
		}
		if len(s.Sessions) < 40 || bytes < 25000 {
			t.Fatalf("%s history too short: %d sessions, %d bytes", s.ID, len(s.Sessions), bytes)
		}
		if len(s.Questions) != len(v1[i].Questions) {
			t.Fatalf("%s question count changed", s.ID)
		}
		for j, q := range s.Questions {
			old := v1[i].Questions[j]
			wantQuery, wantAnswer := old.Query, old.Answer
			if v, ok := revised[q.ID+"/query"]; ok {
				wantQuery = v
			}
			if v, ok := revised[q.ID+"/answer"]; ok {
				wantAnswer = v
			}
			if q.ID != old.ID || q.Query != wantQuery || q.Answer != wantAnswer || q.AskedAt != old.AskedAt || q.Axis != old.Axis || len(q.Evidence) != len(old.Evidence) {
				t.Fatalf("%s changed beyond recorded revisions", q.ID)
			}
		}
	}
}
