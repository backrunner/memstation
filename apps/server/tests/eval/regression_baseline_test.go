package eval

import (
	"encoding/json"
	"fmt"
)

type regressionBaselineProvenance struct {
	Result         externalSource `json:"result"`
	Manifest       externalSource `json:"manifest"`
	Interpretation string         `json:"interpretation"`
}

// No network fallback: a missing or incompatible model baseline must fail
// before ingestion or any paid reader/judge call. Wrong answers and historical
// execution errors are reused verbatim, never selectively refreshed.
func loadRegressionArenaBaselines(plan regressionPlan, groups []regressionGroup, reader LongMemEvalQAJudgeOptions) (map[string]regressionOutcome, error) {
	out := map[string]regressionOutcome{}
	needed := false
	for _, g := range groups {
		if g.Arena != nil {
			needed = true
		}
	}
	if !needed {
		return out, nil
	}
	manifest, err := regressionReadSource(plan.ArenaBaselineManifest)
	if err != nil {
		return nil, fmt.Errorf("pinned Arena baseline manifest required: %w", err)
	}
	var original struct {
		Reader struct {
			Model       string   `json:"model"`
			Provider    string   `json:"provider"`
			Reasoning   string   `json:"reasoning_effort"`
			BaseURL     string   `json:"base_url"`
			Temperature *float64 `json:"temperature"`
		} `json:"reader"`
	}
	if err = json.Unmarshal(manifest, &original); err != nil {
		return nil, err
	}
	if original.Reader.Model != reader.Model || original.Reader.Provider != reader.runInfo().Provider || original.Reader.BaseURL != reader.runInfo().BaseURL || original.Reader.Reasoning != "low" || original.Reader.Temperature == nil || *original.Reader.Temperature != 0 {
		return nil, fmt.Errorf("Arena baseline reader identity mismatch; baseline refresh requires a separate explicit experiment")
	}
	for _, g := range groups {
		if g.Arena == nil {
			continue
		}
		source, ok := plan.ArenaBaselines[g.ID]
		if !ok {
			return nil, fmt.Errorf("missing pinned Arena baseline for %s", g.ID)
		}
		raw, e := regressionReadSource(source)
		if e != nil {
			return nil, e
		}
		var job struct {
			ID      string           `json:"id"`
			Dataset string           `json:"dataset"`
			Mode    string           `json:"mode"`
			Answers []externalAnswer `json:"answers"`
		}
		if e = json.Unmarshal(raw, &job); e != nil {
			return nil, e
		}
		if job.ID != g.ID || job.Dataset != g.Dataset || job.Mode != "none" || len(job.Answers) != len(g.Questions) {
			return nil, fmt.Errorf("Arena baseline chain identity or coverage mismatch")
		}
		for i, q := range g.Questions {
			a := job.Answers[i]
			if a.Index != q.Index || a.Question != q.Query || a.Reference != q.Reference || a.Context != "" || a.Indexed != 0 || len(a.SourceIDs) != 0 || a.WriteError != "" || a.Error == "not executed" || a.Error == "attempt interrupted before completion" || (!a.Judged && a.Error == "") {
				return nil, fmt.Errorf("Arena baseline answer identity, isolation or completion mismatch")
			}
			out[q.ID] = regressionOutcome{Dataset: g.Dataset, Group: g.ID, QuestionID: q.ID, Mode: "none", PriorStatus: q.Prior, Answer: a, ReusedBaseline: &regressionBaselineProvenance{Result: source, Manifest: plan.ArenaBaselineManifest, Interpretation: "Historical model-only control reused without model or judge calls; original prompt/token budget retained; not a same-budget causal ablation"}}
		}
	}
	return out, nil
}

func applyExternalArenaBaselines(plan externalPlan, jobs []*externalJob, reader LongMemEvalQAJudgeOptions) error {
	if plan.CreateArenaBaseline {
		return nil
	}
	var groups []regressionGroup
	for _, job := range jobs {
		if job.Arena == nil || job.Mode != "none" {
			continue
		}
		g := regressionGroup{ID: job.ID, Dataset: job.Dataset, Arena: job.Arena}
		for i, a := range job.Answers {
			g.Questions = append(g.Questions, regressionQuestion{ID: fmt.Sprintf("%s-step-%d", job.ID, i+1), Index: a.Index, Query: a.Question, Reference: a.Reference})
		}
		groups = append(groups, g)
	}
	baselines, err := loadRegressionArenaBaselines(regressionPlan{ArenaBaselines: plan.ArenaBaselines, ArenaBaselineManifest: plan.ArenaBaselineManifest}, groups, reader)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.Arena == nil || job.Mode != "none" {
			continue
		}
		for i := range job.Answers {
			baseline, ok := baselines[fmt.Sprintf("%s-step-%d", job.ID, i+1)]
			if !ok {
				return fmt.Errorf("missing Arena model-only control")
			}
			job.Answers[i] = baseline.Answer
			job.ReusedBaseline = baseline.ReusedBaseline
		}
	}
	return nil
}
