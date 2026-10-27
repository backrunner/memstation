package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRegressionArenaBaselinePreservesFailuresWithoutCalls(t *testing.T) {
	dir := t.TempDir()
	source := func(name string, value any) externalSource {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err = os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return externalSource{Path: p, SHA256: pipelineHash(string(raw))}
	}
	g := regressionGroup{ID: "formal_reasoning_math-1", Dataset: "formal_reasoning_math", Arena: &arenaTask{}, Questions: []regressionQuestion{{ID: "q1", Query: "first", Reference: "a", Index: 0}, {ID: "q2", Query: "second", Reference: "b", Index: 1}}}
	answers := []externalAnswer{{Index: 0, Question: "first", Reference: "a", Answer: "wrong", Judged: true, Correct: false}, {Index: 1, Question: "second", Reference: "b", Answer: "", Error: "reader: output budget exhausted"}}
	plan := regressionPlan{ArenaBaselines: map[string]externalSource{g.ID: source("baseline.json", map[string]any{"id": g.ID, "dataset": g.Dataset, "mode": "none", "answers": answers})}, ArenaBaselineManifest: source("manifest.json", map[string]any{"reader": map[string]any{"provider": "openai_compatible", "model": "fixture", "reasoning_effort": "low", "temperature": 0}})}
	result, err := loadRegressionArenaBaselines(plan, []regressionGroup{g}, LongMemEvalQAJudgeOptions{Model: "fixture"})
	if err != nil || len(result) != 2 {
		t.Fatal(result, err)
	}
	for i, q := range g.Questions {
		if !reflect.DeepEqual(result[q.ID].Answer, answers[i]) || result[q.ID].ReusedBaseline == nil {
			t.Fatal("changed historical control", result[q.ID])
		}
	}
	job := &externalJob{ID: g.ID, Dataset: g.Dataset, Mode: "none", Arena: g.Arena, Answers: []externalAnswer{{Index: 0, Question: "first", Reference: "a"}, {Index: 1, Question: "second", Reference: "b"}}}
	if err := applyExternalArenaBaselines(externalPlan{ArenaBaselines: plan.ArenaBaselines, ArenaBaselineManifest: plan.ArenaBaselineManifest}, []*externalJob{job}, LongMemEvalQAJudgeOptions{Model: "fixture"}); err != nil || !reflect.DeepEqual(job.Answers, answers) || job.ReusedBaseline == nil {
		t.Fatal("full suite failed to reuse control", job, err)
	}
	if _, err = loadRegressionArenaBaselines(plan, []regressionGroup{g}, LongMemEvalQAJudgeOptions{Model: "different"}); err == nil {
		t.Fatal("changed model reused baseline")
	}
	plan.ArenaBaselines[g.ID] = externalSource{Path: plan.ArenaBaselines[g.ID].Path, SHA256: "mismatch"}
	if _, err = loadRegressionArenaBaselines(plan, []regressionGroup{g}, LongMemEvalQAJudgeOptions{Model: "fixture"}); err == nil {
		t.Fatal("tampered control accepted")
	}
	delete(plan.ArenaBaselines, g.ID)
	if _, err = loadRegressionArenaBaselines(plan, []regressionGroup{g}, LongMemEvalQAJudgeOptions{Model: "fixture"}); err == nil {
		t.Fatal("missing control accepted")
	}
}

// Offline provenance verification only. It never resolves a provider or invokes
// a reader/judge, even when manually checking external archived datasets.
func TestRegressionPinnedArenaBaselineAudit(t *testing.T) {
	p := os.Getenv("MEMSTATION_EVAL_REGRESSION_BASELINE_PLAN")
	if p == "" {
		t.Skip("manual offline archive audit")
	}
	raw, err := os.ReadFile(p)
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
	manifest, err := regressionReadSource(plan.ArenaBaselineManifest)
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Reader LongMemEvalQAJudgeOptions `json:"reader"`
	}
	if err = json.Unmarshal(manifest, &original); err != nil {
		t.Fatal(err)
	}
	baselines, err := loadRegressionArenaBaselines(plan, groups, original.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Verified %d historical Arena baseline answers without model calls", len(baselines))
}
