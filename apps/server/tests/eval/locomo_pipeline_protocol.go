package eval

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/provider"
	memoryservice "memstation/apps/server/internal/service/memory"
	rerankservice "memstation/apps/server/internal/service/rerank"
)

// API credentials never enter manifests. ModelRevision identifies deployed weights,
// independently of the mutable model name or endpoint.
type PipelineRerankOptions struct {
	Model               string
	CandidateSelection  string
	ModelRevision       string
	BaseURL             string
	APIKey              string
	Timeout             time.Duration
	CandidateMultiplier int
	MinCandidates       int
	MaxCandidates       int
}

func (o PipelineRerankOptions) manifest() map[string]any {
	return map[string]any{"score_policy": "relevance_v1", "fallback_policy": "hybrid_v1", "model": o.Model, "candidate_selection": o.CandidateSelection, "model_revision": o.ModelRevision, "endpoint_sha256": pipelineHash(o.BaseURL), "timeout_ms": o.Timeout.Milliseconds(), "candidate_multiplier": o.CandidateMultiplier, "min_candidates": o.MinCandidates, "max_candidates": o.MaxCandidates}
}

func configurePipelineReranker(engine *memoryservice.Engine, o PipelineRerankOptions) {
	engine.SetRerankConfig(memoryservice.RerankConfig{Model: o.Model, CandidateSelection: o.CandidateSelection, CandidateMultiplier: o.CandidateMultiplier, MinCandidates: o.MinCandidates, MaxCandidates: o.MaxCandidates})
	if o.Model == "" {
		return
	}
	p, _, err := domain.ParseRerankModel(o.Model)
	if err != nil {
		return
	} // Validated before opening any runtime.
	engine.SetReranker(rerankservice.NewService(provider.StaticConfigResolver{p: {BaseURL: o.BaseURL, APIKey: o.APIKey}}, o.Model, o.Timeout))
}

func pipelineControlMode(mode string) bool {
	return mode == "question_only" || mode == "full_history" || mode == "gold_evidence"
}

func pipelineNeedsIngestion(modes []string) bool {
	for _, mode := range modes {
		if mode == "derived" || mode == "combined" {
			return true
		}
	}
	return false
}

func normalizePipelineProtocol(o *LOCOMOPipelineOptions) error {
	if o.ReaderPolicy == "" {
		o.ReaderPolicy = "baseline"
	}
	if o.ReaderPolicy != "baseline" && o.ReaderPolicy != "coverage" && o.ReaderPolicy != "coverage_inference" && o.ReaderPolicy != "interpretation" {
		return fmt.Errorf("invalid reader policy")
	}

	if o.ContextFormat == "" {
		o.ContextFormat = "plain"
	}
	if o.ContextFormat != "plain" && o.ContextFormat != "evidence" {
		return fmt.Errorf("invalid context format")
	}
	if o.SourceNeighborTurns < 0 || o.SourceNeighborTurns > 2 || (o.ContextFormat == "plain" && o.SourceNeighborTurns != 0) {
		return fmt.Errorf("source neighbors require evidence format and radius zero through two")
	}

	if len(o.Modes) == 0 {
		o.Modes = []string{"raw", "derived", "combined"}
	}
	seen := map[string]bool{}
	retrieval := false
	for _, mode := range o.Modes {
		if seen[mode] || (!pipelineControlMode(mode) && mode != "raw" && mode != "derived" && mode != "combined") {
			return fmt.Errorf("invalid or duplicate evaluation mode")
		}
		if o.RefineEvidence && pipelineControlMode(mode) {
			return fmt.Errorf("evidence refinement cannot run on oracle controls")
		}
		seen[mode] = true
		retrieval = retrieval || !pipelineControlMode(mode)
	}
	if o.OracleContextBytes == 0 {
		o.OracleContextBytes = 512000
	}
	if o.OracleContextBytes < 256 || o.OracleNeighborTurns < 0 || o.OracleNeighborTurns > 10 {
		return fmt.Errorf("invalid oracle context budget or neighbor radius")
	}
	var err error
	if retrieval {
		if o.EmbeddingRevision == "" {
			return fmt.Errorf("embedding revision required for retrieval")
		}
		o.Embedding, err = normalizeEmbeddingOptions(o.Embedding)
		if err != nil || o.Embedding.Mode != EmbeddingModeProvider {
			return fmt.Errorf("real provider embeddings required")
		}
	}
	if pipelineNeedsIngestion(o.Modes) {
		o.Extraction, err = normalizeLongMemEvalQAJudgeOptions(o.Extraction)
		if err != nil {
			return err
		}
	}
	o.Answerer, err = normalizeLongMemEvalQAJudgeOptions(o.Answerer)
	if err != nil {
		return err
	}
	if o.RefineEvidence {
		if o.Planner.Model == "" {
			o.Planner = o.Answerer
		}
		o.Planner, err = normalizeLongMemEvalQAJudgeOptions(o.Planner)
		if err != nil {
			return err
		}
	}
	o.Judge, err = normalizeLongMemEvalQAJudgeOptions(o.Judge)
	if err != nil {
		return err
	}
	r := &o.Rerank
	r.Model = strings.TrimSpace(r.Model)
	if r.Timeout == 0 {
		r.Timeout = 60 * time.Second
	}
	defaults := memoryservice.DefaultRerankConfig()
	if r.CandidateSelection == "" {
		r.CandidateSelection = defaults.CandidateSelection
	}
	if r.CandidateSelection != "balanced" && r.CandidateSelection != "preliminary" {
		return fmt.Errorf("invalid rerank candidate selection")
	}
	if r.CandidateMultiplier == 0 {
		r.CandidateMultiplier = defaults.CandidateMultiplier
	}
	if r.MinCandidates == 0 {
		r.MinCandidates = defaults.MinCandidates
	}
	if r.MaxCandidates == 0 {
		r.MaxCandidates = defaults.MaxCandidates
	}

	if r.Timeout < 0 || r.CandidateMultiplier < 1 || r.CandidateMultiplier > 16 || r.MinCandidates < 1 || r.MaxCandidates < r.MinCandidates || r.MaxCandidates > 200 {
		return fmt.Errorf("invalid rerank budgets")
	}
	if r.Model == "" {
		if r.BaseURL != "" || r.ModelRevision != "" {
			return fmt.Errorf("rerank endpoint/revision requires a model")
		}
		return nil
	}
	p, _, err := domain.ParseRerankModel(r.Model)
	if err != nil {
		return err
	}
	u, err := url.Parse(r.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid rerank endpoint")
	}
	if r.ModelRevision == "" || (!domain.RerankAPIKeyOptional(p) && strings.TrimSpace(r.APIKey) == "") {
		return fmt.Errorf("rerank requires model revision and provider credentials")
	}

	return nil
}

// Gold IDs are consulted only in this explicitly diagnostic path. No reference
// answer or category is included in any reader input. Neighbors stay in-session.
func pipelineControlContext(s pipelineSample, q pipelineQuestion, mode string, budget, radius int) (string, []string, error) {
	if mode == "question_only" {
		return "", nil, nil
	}
	wanted := map[string]bool{}
	if mode == "gold_evidence" {
		if len(q.Evidence) == 0 {
			return "", nil, fmt.Errorf("oracle evidence unavailable")
		}
		for _, ref := range q.Evidence {
			wanted[ref] = false
		}
	} else if mode != "full_history" {
		return "", nil, fmt.Errorf("unknown control mode")
	}
	var out strings.Builder
	refs := []string{}
	for _, session := range s.Sessions {
		selected := map[int]bool{}
		for i, ref := range session.Refs {
			if _, ok := wanted[ref]; ok {
				wanted[ref] = true
				for j := max(0, i-radius); j <= min(len(session.Refs)-1, i+radius); j++ {
					selected[j] = true
				}
			}
		}
		for i, m := range session.Messages {
			if mode != "full_history" && !selected[i] {
				continue
			}
			if i >= len(session.Refs) {
				return "", nil, fmt.Errorf("invalid source reference mapping")
			}
			line := fmt.Sprintf("[source %s] %s\n", session.Refs[i], m.Text)
			if out.Len()+len(line) > budget {
				return "", nil, fmt.Errorf("oracle context exceeds byte limit; refusing truncated baseline")
			}
			out.WriteString(line)
			refs = append(refs, session.Refs[i])
		}
	}
	for _, found := range wanted {
		if !found {
			return "", nil, fmt.Errorf("oracle evidence references unresolved; refusing partial baseline")
		}
	}
	return out.String(), refs, nil
}

// Advisory lock is released on process exit, including a crash. The stable lock
// inode is never removed, so concurrent resumes cannot lock different files.
func lockPipelineOutput(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".run.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another pipeline owns this output directory")
	}
	return f, nil
}

type PipelineOracleComparison struct {
	Paired               int      `json:"paired_successful_judgments"`
	ReferenceCorrect     int      `json:"reference_correct"`
	BothCorrect          int      `json:"both_correct"`
	RetrievalOnlyCorrect int      `json:"retrieval_only_correct"`
	Retention            *float64 `json:"retention_of_reference_correct,omitempty"`
}

func refreshPipelineSummaries(report *LOCOMOPipelineReport) {
	report.Modes = summarizePipelineAnswers(report.Answers)
	report.BySample = map[string]map[string]PipelineModeSummary{}
	report.ByCategory = map[int]map[string]PipelineModeSummary{}
	samples := map[string][]PipelineAnswer{}
	categories := map[int][]PipelineAnswer{}
	indexed := map[string]map[string]PipelineAnswer{}
	for _, a := range report.Answers {
		samples[a.SampleID] = append(samples[a.SampleID], a)
		categories[a.Category] = append(categories[a.Category], a)
		key := a.SampleID + "/" + a.QuestionID
		if indexed[key] == nil {
			indexed[key] = map[string]PipelineAnswer{}
		}
		indexed[key][a.Mode] = a
	}
	for key, answers := range samples {
		report.BySample[key] = summarizePipelineAnswers(answers)
	}
	for key, answers := range categories {
		report.ByCategory[key] = summarizePipelineAnswers(answers)
	}
	report.OracleComparisons = map[string]PipelineOracleComparison{}
	for _, row := range indexed {
		for _, referenceMode := range []string{"full_history", "gold_evidence"} {
			reference, ok := row[referenceMode]
			if !ok || !reference.Judged || reference.Error != "" {
				continue
			}
			for _, mode := range []string{"raw", "derived", "combined"} {
				candidate, ok := row[mode]
				if !ok || !candidate.Judged || candidate.Error != "" {
					continue
				}
				key := mode + "_vs_" + referenceMode
				v := report.OracleComparisons[key]
				v.Paired++
				if reference.Correct {
					v.ReferenceCorrect++
					if candidate.Correct {
						v.BothCorrect++
					}
				} else if candidate.Correct {
					v.RetrievalOnlyCorrect++
				}
				report.OracleComparisons[key] = v
			}
		}
	}
	for key, v := range report.OracleComparisons {
		if v.ReferenceCorrect > 0 {
			x := float64(v.BothCorrect) / float64(v.ReferenceCorrect)
			v.Retention = &x
		}
		report.OracleComparisons[key] = v
	}
}
