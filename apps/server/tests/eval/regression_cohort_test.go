package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
)

type regressionSelection struct {
	ID          string `json:"id"`
	SampleID    string `json:"sample_id"`
	Dataset     string `json:"dataset"`
	TaskID      int    `json:"task_id"`
	Steps       int    `json:"steps"`
	PriorStatus string `json:"prior_status"`
}
type regressionPlan struct {
	ArenaBaselines        map[string]externalSource `json:"arena_baselines"`
	ArenaBaselineManifest externalSource            `json:"arena_baseline_manifest"`
	Version               int                       `json:"version"`
	Scope                 string                    `json:"scope"`
	Locomo                externalSource            `json:"locomo"`
	LongMem               externalSource            `json:"longmemeval"`
	Arena                 []externalSource          `json:"arena"`
	Selection             struct {
		Locomo  []regressionSelection `json:"locomo"`
		LongMem []regressionSelection `json:"longmemeval"`
		Arena   []regressionSelection `json:"arena"`
	} `json:"selection"`
	SelectionSources  []externalSource  `json:"selection_sources"`
	Modes             []string          `json:"modes"`
	ReaderTokens      int               `json:"reader_output_tokens"`
	MaxTokens         int               `json:"max_output_tokens"`
	ContextBytes      int               `json:"context_bytes"`
	TopN              int               `json:"top_n"`
	RerankCandidates  int               `json:"rerank_candidates"`
	ReaderThinking    map[string]string `json:"reader_thinking"`
	WorkerThinking    string            `json:"worker_thinking"`
	Concurrency       int               `json:"concurrency"`
	ExpectedQuestions int               `json:"expected_questions"`
	ExpectedAnswers   int               `json:"expected_answers"`
}
type regressionQuestion struct {
	ID        string
	Query     string
	Reference string
	Index     int
	Date      string
	Prior     string
	LongMem   *longMemEvalSample
	Locomo    *pipelineQuestion
}
type regressionGroup struct {
	ID        string
	Dataset   string
	Sessions  []pipelineSession
	Questions []regressionQuestion
	Arena     *arenaTask
	LongMem   *longMemEvalSample
	Locomo    *pipelineSample
}

func regressionReadSource(source externalSource) ([]byte, error) {
	raw, err := os.ReadFile(source.Path)
	if err != nil {
		return nil, err
	}
	if pipelineHash(string(raw)) != source.SHA256 {
		return nil, fmt.Errorf("source provenance mismatch")
	}
	return raw, nil
}

func loadRegressionGroups(plan regressionPlan) ([]regressionGroup, error) {
	if plan.Version != 1 || plan.ExpectedQuestions < 1 || plan.ExpectedAnswers != plan.ExpectedQuestions*4 || strings.Join(plan.Modes, ",") != "none,raw,derived,combined" || plan.ReaderTokens < 1 || plan.ReaderTokens > plan.MaxTokens || plan.MaxTokens > 262144 || plan.ContextBytes < 1 || plan.TopN < 1 || plan.Concurrency < 1 || plan.Concurrency > 8 || plan.RerankCandidates < plan.TopN || plan.RerankCandidates > 200 {
		return nil, fmt.Errorf("invalid regression plan")
	}
	for _, s := range plan.SelectionSources {
		if _, err := regressionReadSource(s); err != nil {
			return nil, err
		}
	}
	if _, err := regressionReadSource(plan.Locomo); err != nil {
		return nil, err
	}
	samples, err := loadPipelineSamples(plan.Locomo.Path, "", 0)
	if err != nil {
		return nil, err
	}
	var groups []regressionGroup
	locomo := map[string]*regressionGroup{}
	seen := map[string]bool{}
	for _, selected := range plan.Selection.Locomo {
		var sample *pipelineSample
		var question *pipelineQuestion
		for i := range samples {
			if samples[i].ID == selected.SampleID {
				sample = &samples[i]
				for j := range sample.Questions {
					if sample.Questions[j].ID == selected.ID {
						question = &sample.Questions[j]
						break
					}
				}
				break
			}
		}
		if sample == nil || question == nil || seen[selected.ID] {
			return nil, fmt.Errorf("invalid or duplicate LOCOMO selection")
		}
		seen[selected.ID] = true
		g := locomo[sample.ID]
		if g == nil {
			g = &regressionGroup{ID: "locomo-" + sample.ID, Dataset: "locomo", Sessions: sample.Sessions, Locomo: sample}
			locomo[sample.ID] = g
		}
		g.Questions = append(g.Questions, regressionQuestion{ID: question.ID, Query: question.Query, Reference: question.Answer, Prior: selected.PriorStatus, Locomo: question})
	}
	// Input order, not map order, fixes execution and manifests.
	for _, sample := range samples {
		if g := locomo[sample.ID]; g != nil {
			groups = append(groups, *g)
		}
	}
	raw, err := regressionReadSource(plan.LongMem)
	if err != nil {
		return nil, err
	}
	var longmem []longMemEvalSample
	if err = json.Unmarshal(raw, &longmem); err != nil {
		return nil, err
	}
	for _, selected := range plan.Selection.LongMem {
		var sample *longMemEvalSample
		for i := range longmem {
			if longmem[i].QuestionID == selected.ID {
				sample = &longmem[i]
				break
			}
		}
		if sample == nil || seen[selected.ID] || len(sample.HaystackSessions) != len(sample.HaystackDates) || len(sample.HaystackSessions) != len(sample.HaystackSessionIDs) {
			return nil, fmt.Errorf("invalid LongMemEval selection")
		}
		seen[selected.ID] = true
		g := regressionGroup{ID: "longmemeval-" + sample.QuestionID, Dataset: "longmemeval_s", LongMem: sample}
		g.Questions = []regressionQuestion{{ID: sample.QuestionID, Query: sample.Question, Reference: normalizeAnyString(sample.Answer), Date: normalizeAnyString(sample.QuestionDate), Prior: selected.PriorStatus, LongMem: sample}}
		for i, turns := range sample.HaystackSessions {
			session := pipelineSession{ID: fmt.Sprintf("session-%03d", i+1), Date: normalizeAnyString(sample.HaystackDates[i]), At: longMemEvalSessionTime(*sample, i, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))}
			for _, turn := range turns {
				if turn.Role != "user" && turn.Role != "assistant" {
					return nil, fmt.Errorf("unsupported history role")
				}
				session.Messages = append(session.Messages, domain.UnifiedMessage{Role: domain.MessageRole(turn.Role), Text: turn.Content})
			}
			g.Sessions = append(g.Sessions, session)
		}
		sort.SliceStable(g.Sessions, func(i, j int) bool { return g.Sessions[i].At.Before(g.Sessions[j].At) })
		groups = append(groups, g)
	}
	arena := map[string][]arenaTask{}
	for _, source := range plan.Arena {
		raw, err := regressionReadSource(source)
		if err != nil {
			return nil, err
		}
		tasks, err := loadArenaTasks(raw)
		if err != nil {
			return nil, err
		}
		arena[source.Name] = tasks
	}
	for _, selected := range plan.Selection.Arena {
		var task *arenaTask
		for i := range arena[selected.Dataset] {
			if arena[selected.Dataset][i].ID == selected.TaskID {
				task = &arena[selected.Dataset][i]
				break
			}
		}
		if task == nil || selected.Steps != len(task.Questions) || seen[selected.ID] || selected.ID != fmt.Sprintf("%s-%d", selected.Dataset, selected.TaskID) {
			return nil, fmt.Errorf("invalid complete Arena chain")
		}
		seen[selected.ID] = true
		g := regressionGroup{ID: selected.ID, Dataset: selected.Dataset, Arena: task}
		for i, q := range task.Questions {
			g.Questions = append(g.Questions, regressionQuestion{ID: fmt.Sprintf("%s-step-%d", selected.ID, i+1), Query: q, Reference: task.Answers[i], Index: i, Prior: selected.PriorStatus})
		}
		groups = append(groups, g)
	}
	count := 0
	for _, g := range groups {
		count += len(g.Questions)
		if plan.ReaderThinking[g.Dataset] != "low" && plan.ReaderThinking[g.Dataset] != "high" {
			return nil, fmt.Errorf("explicit supported reader thinking required")
		}
	}
	if count != plan.ExpectedQuestions {
		return nil, fmt.Errorf("question denominator changed: %d", count)
	}
	return groups, nil
}
