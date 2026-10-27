package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestRegressionCohortRejectsPartialChainsAndDoesNotIngestLabels(t *testing.T) {
	dir := t.TempDir()
	source := func(name string, value any) externalSource {
		t.Helper()
		path := filepath.Join(dir, name)
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return externalSource{Path: path, SHA256: pipelineHash(string(raw))}
	}
	locomoPath := pipelineFixture(t)
	locomoRaw, err := os.ReadFile(locomoPath)
	if err != nil {
		t.Fatal(err)
	}
	locomo := externalSource{Path: locomoPath, SHA256: pipelineHash(string(locomoRaw))}
	history := source("longmem.json", []longMemEvalSample{{QuestionID: "fixture", Question: "QUERY_ONLY", Answer: "GOLD_ONLY", QuestionType: "single-session-user", HaystackSessionIDs: []any{"source-a", "source-b"}, HaystackDates: []any{"2025-03-01T10:00:00Z", "2025-01-01T10:00:00Z"}, HaystackSessions: [][]longMemEvalTurn{{{Role: "user", Content: "original observed source", HasAnswer: true}}, {{Role: "assistant", Content: "original older reply"}}}, AnswerSessionIDs: []any{"source-a"}}})
	arena := source("arena.jsonl", arenaTask{ID: 1, Paper: "fixture-paper", Questions: []string{"first task", "later task"}, Answers: []string{"GOLD_A", "GOLD_B"}, Backgrounds: []string{"current setup", "FUTURE_SETUP"}})
	arena.Name = "formal_reasoning_math"
	plan := regressionPlan{Version: 1, Locomo: locomo, LongMem: history, Arena: []externalSource{arena}, Modes: []string{"none", "raw", "derived", "combined"}, ReaderTokens: 16384, MaxTokens: 65536, ContextBytes: 128000, TopN: 10, RerankCandidates: 50, Concurrency: 1, ExpectedQuestions: 3, ExpectedAnswers: 12, ReaderThinking: map[string]string{"longmemeval_s": "high", "formal_reasoning_math": "low"}}
	plan.Selection.LongMem = []regressionSelection{{ID: "fixture"}}
	plan.Selection.Arena = []regressionSelection{{ID: "formal_reasoning_math-1", Dataset: "formal_reasoning_math", TaskID: 1, Steps: 2}}
	groups, err := loadRegressionGroups(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups[0].Sessions) != 2 || groups[0].Sessions[0].ID != "session-002" {
		t.Fatal("history ingestion is not chronological", groups[0].Sessions)
	}
	raw, err := json.Marshal(groups[0].Sessions)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"GOLD_ONLY", "QUERY_ONLY", "has_answer", "answer_session_ids", "question_type"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("worker source leaked %s", forbidden)
		}
	}
	data := regressionRawDataset(groups[0], "owner", Dataset{})
	for _, doc := range data.Documents {
		if len(doc.Metadata) != 1 || doc.Metadata["session_date"] == nil {
			t.Fatal("raw retrieval metadata leaked labels", doc.Metadata)
		}
	}
	plan.Selection.Arena[0].Steps = 1
	if _, err = loadRegressionGroups(plan); err == nil {
		t.Fatal("partial chain accepted")
	}
	plan.Selection.Arena[0].Steps = 2
	plan.Selection.Arena = append(plan.Selection.Arena, plan.Selection.Arena[0])
	plan.ExpectedQuestions = 5
	plan.ExpectedAnswers = 20
	if _, err = loadRegressionGroups(plan); err == nil {
		t.Fatal("duplicate chain accepted")
	}
}

func TestRegressionContextPreservesSourceTimeAndBudgetsWholeRecords(t *testing.T) {
	docs := []domain.MemoryDocument{{ID: "a", Content: "A plan, not an accomplished event", Metadata: map[string]any{"source_session_time": "2025-03-04", "valid_from": "2025-03-10T09:00:00Z", "has_answer": true}}}
	text, admitted, omitted := regressionContext(docs, 4096)
	if !strings.Contains(text, "source_session_time") || !strings.Contains(text, "2025-03-04") || strings.Contains(text, "has_answer") || len(admitted) != 1 || omitted != 0 {
		t.Fatal("lost temporal provenance or rendered a benchmark label", text, admitted, omitted)
	}
	text, admitted, omitted = regressionContext(docs, 10)
	if text != "" || len(admitted) != 0 || omitted != 1 {
		t.Fatal("record was silently truncated", text, admitted, omitted)
	}
}
