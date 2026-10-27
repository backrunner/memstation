package eval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/ports"
	"memstation/apps/server/internal/service/analysis"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/memoryllm"
)

// Pipeline options are deliberately separate from the raw-turn retrieval track.
// The database must be dedicated: the relay consumes its pending outbox.
type LOCOMOPipelineOptions struct {
	TaskConcurrency      int
	AnswerOutputTokens   int
	extractionAdapter    ports.ProviderAdapter
	ReaderPolicy         string
	SemanticEpisodes     bool
	RefineEvidence       bool
	Planner              LongMemEvalQAJudgeOptions
	ContextFormat        string
	SourceNeighborTurns  int
	Modes                []string
	OracleContextBytes   int
	OracleNeighborTurns  int
	Rerank               PipelineRerankOptions
	DatasetPath          string
	DatabaseURL          string
	OutputDir            string
	Prefix               string
	SampleID             string
	QuestionsPerCategory int
	TopN                 int
	ContextBytes         int
	CodeRevision         string
	EmbeddingRevision    string
	Embedding            EmbeddingOptions
	Extraction           LongMemEvalQAJudgeOptions
	Answerer             LongMemEvalQAJudgeOptions
	Judge                LongMemEvalQAJudgeOptions
	manifestHash         string
}
type pipelineSession struct {
	ID       string                  `json:"id"`
	Date     string                  `json:"date"`
	At       time.Time               `json:"at"`
	Messages []domain.UnifiedMessage `json:"messages"`
	Refs     []string                `json:"refs"`
}
type pipelineQuestion struct {
	ID       string   `json:"id"`
	Query    string   `json:"query"`
	Answer   string   `json:"answer"`
	Category int      `json:"category"`
	Evidence []string `json:"evidence"`
}
type pipelineSample struct {
	ID        string
	Sessions  []pipelineSession
	Questions []pipelineQuestion
}
type PipelineStage struct {
	SessionID      string                  `json:"session_id"`
	ConversationID string                  `json:"conversation_id"`
	Seconds        float64                 `json:"seconds"`
	Documents      []domain.MemoryDocument `json:"documents,omitempty"`
	Error          string                  `json:"error,omitempty"`
}
type PipelineAnswer struct {
	RawAnswer        string                              `json:"raw_answer,omitempty"`
	RawJudgment      string                              `json:"raw_judgment,omitempty"`
	ContextSourceIDs map[string]string                   `json:"context_source_ids,omitempty"`
	Refinement       memoryservice.RefinementDiagnostics `json:"refinement"`
	PlannerUsage     domain.UsageRecord                  `json:"planner_usage"`
	PlannerModel     string                              `json:"planner_model_returned,omitempty"`
	Evidence         memoryservice.EvidenceDiagnostics   `json:"evidence_packet"`
	EvidenceMillis   float64                             `json:"evidence_millis"`
	ManifestHash     string                              `json:"manifest_sha256"`
	ContextHash      string                              `json:"context_sha256"`
	AnswerModel      string                              `json:"answer_model_returned,omitempty"`
	JudgeModel       string                              `json:"judge_model_returned,omitempty"`
	DiagnosticOnly   bool                                `json:"diagnostic_only"`
	Rerank           memoryservice.RerankDiagnostics     `json:"rerank"`
	SampleID         string                              `json:"sample_id"`
	QuestionID       string                              `json:"question_id"`
	Category         int                                 `json:"category"`
	Mode             string                              `json:"mode"`
	Query            string                              `json:"query"`
	Expected         string                              `json:"expected"`
	Answer           string                              `json:"answer"`
	Correct          bool                                `json:"correct"`
	Judged           bool                                `json:"judged"`
	JudgeReason      string                              `json:"judge_reason,omitempty"`
	Error            string                              `json:"error,omitempty"`
	Context          string                              `json:"context"`
	ContextBytes     int                                 `json:"context_bytes"`
	ContextDocuments int                                 `json:"context_documents"`
	SourceRefs       []string                            `json:"source_refs,omitempty"`
	EvidenceRefs     []string                            `json:"evidence_refs,omitempty"`
	CitationRecall   *float64                            `json:"citation_recall,omitempty"`
	SearchMillis     float64                             `json:"search_millis"`
	AnswerMillis     float64                             `json:"answer_millis"`
	JudgeMillis      float64                             `json:"judge_millis"`
	AnswerUsage      domain.UsageRecord                  `json:"answer_usage"`
	JudgeUsage       domain.UsageRecord                  `json:"judge_usage"`
	Retrieval        any                                 `json:"retrieval,omitempty"`
}
type PipelineModeSummary struct {
	MeanPlannerInputTokens  float64        `json:"mean_planner_input_tokens"`
	MeanPlannerOutputTokens float64        `json:"mean_planner_output_tokens"`
	EvidenceReadyP95Millis  float64        `json:"evidence_ready_p95_millis"`
	RefinementStatuses      map[string]int `json:"refinement_statuses"`
	FollowupRerankStatuses  map[string]int `json:"followup_rerank_statuses"`
	RerankStatuses          map[string]int `json:"rerank_statuses"`
	MeanAnswerInputTokens   float64        `json:"mean_answer_input_tokens"`
	Expected                int            `json:"expected"`
	Judged                  int            `json:"judged"`
	Correct                 int            `json:"correct"`
	Errors                  int            `json:"errors"`
	Accuracy                float64        `json:"accuracy_all_expected"`
	Coverage                float64        `json:"judge_coverage"`
	MeanContextBytes        float64        `json:"mean_context_bytes"`
	SearchP95Millis         float64        `json:"search_p95_millis"`
}
type LOCOMOPipelineReport struct {
	BySample          map[string]map[string]PipelineModeSummary `json:"by_sample"`
	ByCategory        map[int]map[string]PipelineModeSummary    `json:"by_category"`
	OracleComparisons map[string]PipelineOracleComparison       `json:"oracle_comparisons"`
	GeneratedAt       time.Time                                 `json:"generated_at"`
	Manifest          map[string]any                            `json:"manifest"`
	ExpectedSessions  int                                       `json:"expected_sessions"`
	CompletedSessions int                                       `json:"completed_sessions"`
	Stages            []PipelineStage                           `json:"stages"`
	Answers           []PipelineAnswer                          `json:"answers"`
	Modes             map[string]PipelineModeSummary            `json:"modes"`
}

func loadPipelineSamples(path, sampleID string, perCategory int) ([]pipelineSample, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var samples []locomoSample
	if err = json.Unmarshal(raw, &samples); err != nil {
		return nil, err
	}
	out := []pipelineSample{}
	seen := map[string]bool{}
	for _, s := range samples {
		if !safePipelineID(s.SampleID) || seen[s.SampleID] {
			return nil, fmt.Errorf("empty or duplicate sample identity")
		}
		seen[s.SampleID] = true
		if sampleID != "" && s.SampleID != sampleID {
			continue
		}
		p := pipelineSample{ID: s.SampleID}
		source, ok := s.Conversation.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("pipeline requires dated LOCOMO sessions")
		}
		refs := map[string]bool{}
		for key, value := range source {
			if !isLOCOMOSessionKey(key) {
				continue
			}
			turns, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("invalid session %s", key)
			}
			date := firstNonEmptyString(source, key+"_date_time")
			at, err := parsePipelineDate(date)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", s.SampleID, key, err)
			}
			session := pipelineSession{ID: key, Date: date, At: at}
			for _, rawTurn := range turns {
				turn, ok := rawTurn.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid turn")
				}
				id, speaker, text := firstNonEmptyString(turn, "dia_id"), firstNonEmptyString(turn, "speaker"), firstNonEmptyString(turn, "text")
				if id == "" || speaker == "" || text == "" || refs[id] {
					return nil, fmt.Errorf("invalid or duplicate source turn in %s", s.SampleID)
				}
				refs[id] = true
				// Both speakers are human sources. Names/time remain source text, not an
				// authorization identity. Gold QA and machine captions never enter ingest.
				session.Messages = append(session.Messages, domain.UnifiedMessage{Role: domain.RoleUser, Metadata: map[string]any{"ref": id, "speaker": speaker}, Text: fmt.Sprintf("[Conversation date: %s] [Speaker: %s] %s", date, speaker, text)})
				session.Refs = append(session.Refs, id)
			}
			if len(session.Messages) == 0 {
				return nil, fmt.Errorf("empty session")
			}
			p.Sessions = append(p.Sessions, session)
		}
		sort.Slice(p.Sessions, func(i, j int) bool {
			if p.Sessions[i].At.Equal(p.Sessions[j].At) {
				return p.Sessions[i].ID < p.Sessions[j].ID
			}
			return p.Sessions[i].At.Before(p.Sessions[j].At)
		})
		for i, q := range s.QA {
			category := intValue(q["category"])
			if category == 5 {
				continue
			}
			query, answer := firstNonEmptyString(q, "question"), normalizeAnyString(q["answer"])
			if category < 1 || category > 4 || query == "" || answer == "" {
				return nil, fmt.Errorf("invalid QA item in %s", s.SampleID)
			}
			p.Questions = append(p.Questions, pipelineQuestion{ID: fmt.Sprintf("%s.%d", s.SampleID, i+1), Query: query, Answer: answer, Category: category, Evidence: collectEvidenceRefs(q)})
		}
		if perCategory > 0 {
			// Sampling is based only on IDs, before scores; preserve all source sessions.
			sort.Slice(p.Questions, func(i, j int) bool { return pipelineHash(p.Questions[i].ID) < pipelineHash(p.Questions[j].ID) })
			counts := map[int]int{}
			selected := []pipelineQuestion{}
			for _, q := range p.Questions {
				if counts[q.Category] < perCategory {
					selected = append(selected, q)
					counts[q.Category]++
				}
			}
			p.Questions = selected
		}
		sort.Slice(p.Questions, func(i, j int) bool { return p.Questions[i].ID < p.Questions[j].ID })
		if len(p.Sessions) == 0 || len(p.Questions) == 0 {
			return nil, fmt.Errorf("empty sample selection")
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no matching samples")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func isLOCOMOSessionKey(key string) bool {
	if !strings.HasPrefix(key, "session_") || strings.HasSuffix(key, "_date_time") {
		return false
	}
	for _, r := range strings.TrimPrefix(key, "session_") {
		if r < '0' || r > '9' {
			return false
		}
	}
	return key != "session_"
}

func safePipelineID(id string) bool {
	if id == "" || len(id) > 100 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func parsePipelineDate(value string) (time.Time, error) {
	for _, layout := range []string{"3:04 pm on 2 January, 2006", "15:04 on 2 January, 2006", time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized source date")
}
func pipelineHash(v string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(v))) }
func writePipelineJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func RunLOCOMOPipeline(ctx context.Context, o LOCOMOPipelineOptions) (LOCOMOPipelineReport, error) {
	if o.AnswerOutputTokens == 0 {
		o.AnswerOutputTokens = memoryllm.DefaultMinOutputTokens
	}

	var report LOCOMOPipelineReport
	if !strings.HasPrefix(o.Prefix, "eval-") || !safePipelineID(o.Prefix) || o.OutputDir == "" || o.CodeRevision == "" || o.TopN < 1 || o.ContextBytes < 256 || o.QuestionsPerCategory < 0 {
		return report, fmt.Errorf("pipeline requires eval- prefix, private output, pinned code/model revisions and positive budgets")
	}
	lease, err := lockPipelineOutput(o.OutputDir)
	if err != nil {
		return report, err
	}
	defer func() { _ = lease.Close() }()
	samples, err := loadPipelineSamples(o.DatasetPath, o.SampleID, o.QuestionsPerCategory)
	if err != nil {
		return report, err
	}
	if err = normalizePipelineProtocol(&o); err != nil {
		return report, err
	}
	embedding := o.Embedding
	raw, err := os.ReadFile(o.DatasetPath)
	if err != nil {
		return report, err
	}
	manifest := map[string]any{"version": 3, "qa_output_tokens": o.AnswerOutputTokens, "output_budget_retry": "one explicit truncation retry to the configured output ceiling", "dataset_sha256": pipelineHash(string(raw)), "code_revision": o.CodeRevision, "embedding_revision": o.EmbeddingRevision, "prefix": o.Prefix, "sample_id": o.SampleID, "questions_per_category": o.QuestionsPerCategory, "top_n": o.TopN, "context_byte_budget": o.ContextBytes, "embedding": embedding.runInfo(), "extraction": o.Extraction.runInfo(), "answerer": o.Answerer.runInfo(), "judge": o.Judge.runInfo(), "task_concurrency": max(o.TaskConcurrency, 1), "transport": "production PostgreSQL + outbox relay + Worker handlers; bounded workers with one durable run-wide local task journal; no HTTP/Redis transport measurement", "image_policy": "raw text only; no machine captions or image search query", "sampling": "SHA256(question ID) within category; all source sessions", "source_timezone": "dataset does not specify zone; parsed as UTC for ordering; original date retained", "judge_protocol": "memstation-locomo-qa-v2-reference-agreement; not a vendor leaderboard reproduction"}
	manifest["modes"] = o.Modes
	manifest["context_format"] = o.ContextFormat
	manifest["reader_policy"] = o.ReaderPolicy
	manifest["reader_format"] = "plain_text_v1"
	manifest["semantic_episodes"] = o.SemanticEpisodes
	if o.SemanticEpisodes {
		manifest["semantic_episode_policy_sha256"] = analysis.SemanticEpisodePolicyHash()
	}
	manifest["refine_evidence"] = o.RefineEvidence
	if o.RefineEvidence {
		manifest["planner"] = o.Planner.runInfo()
		manifest["planner_prompt_sha256"] = pipelineHash(memoryservice.EvidencePlanInstruction)
		manifest["selector_prompt_sha256"] = pipelineHash(memoryservice.EvidenceSelectionInstruction)
		manifest["refinement_policy"] = "additive_coverage_v2; question-only plan, initial and follow-up scored pools before page cutoff, at most three pools of50, interleaved union before bounded selector; source selection prioritizes additions, preserve original fitted evidence, final context shares the same byte budget;180s overall"
		manifest["selection_byte_budget"] = min(32000, max(1024, o.ContextBytes*2))
	}
	manifest["source_neighbor_turns"] = o.SourceNeighborTurns
	if o.ContextFormat == "evidence" {
		manifest["context_admission"] = "scored_candidate_pool_byte_budget_v1"
	}
	manifest["evidence_packet_policy"] = "v1: provenance and whole records; core reservation two thirds, remaining source context, unused budget reclaimed; max 20 anchors"
	manifest["timeouts_ms"] = map[string]int64{"extraction": o.Extraction.Timeout.Milliseconds(), "answerer": o.Answerer.Timeout.Milliseconds(), "judge": o.Judge.Timeout.Milliseconds()}
	manifest["generation_revision_policy"] = "configured model IDs and returned model IDs are recorded; remote model weights are not independently attested"
	manifest["oracle_context_byte_limit"] = o.OracleContextBytes
	manifest["oracle_neighbor_turns"] = o.OracleNeighborTurns
	manifest["oracle_policy"] = "diagnostic only; gold references never enter retrieval or ingestion; full contexts fail rather than truncate"
	manifest["answer_prompt_sha256"] = pipelineHash(pipelineReaderInstruction(o.ReaderPolicy))
	manifest["judge_prompt_sha256"] = pipelineHash(pipelineJudgeInstruction)
	manifest["rerank"] = o.Rerank.manifest()
	manifest["checkpoint_policy"] = "each completed or failed attempt is immutable; resume only absent checkpoints; repeats require a new run"
	if !pipelineNeedsIngestion(o.Modes) {
		manifest["transport"] = "read-only controls/raw retrieval; no database or ingestion"
	}
	// Public endpoint metadata omits paths; pin their identity separately so a
	// different proxy route on the same host cannot silently reuse checkpoints.
	manifest["endpoint_sha256"] = map[string]string{"embedding": pipelineHash(strings.TrimSpace(o.Embedding.BaseURL)), "extraction": pipelineHash(strings.TrimSpace(o.Extraction.BaseURL)), "answerer": pipelineHash(strings.TrimSpace(o.Answerer.BaseURL)), "judge": pipelineHash(strings.TrimSpace(o.Judge.BaseURL))}
	manifestPath := filepath.Join(o.OutputDir, "manifest.json")
	if previous, readErr := os.ReadFile(manifestPath); readErr == nil {
		var v any
		if json.Unmarshal(previous, &v) != nil {
			return report, fmt.Errorf("invalid checkpoint manifest")
		}
		a, _ := json.Marshal(v)
		encoded, _ := json.Marshal(manifest)
		var current any
		_ = json.Unmarshal(encoded, &current)
		b, _ := json.Marshal(current)
		if string(a) != string(b) {
			return report, fmt.Errorf("manifest mismatch: use a new output directory and eval prefix")
		}
	} else if !os.IsNotExist(readErr) {
		return report, readErr
	}
	if err = writePipelineJSON(manifestPath, manifest); err != nil {
		return report, err
	}
	manifestJSON, _ := json.Marshal(manifest)
	o.manifestHash = pipelineHash(string(manifestJSON))
	runtime, err := newPipelineRuntime(ctx, o)
	if err != nil {
		return report, err
	}
	defer runtime.close()
	report = LOCOMOPipelineReport{GeneratedAt: time.Now().UTC(), Manifest: manifest}
	for _, s := range samples {
		if pipelineNeedsIngestion(o.Modes) {
			report.ExpectedSessions += len(s.Sessions)
		}
		for _, q := range s.Questions {
			for _, mode := range o.Modes {
				report.Answers = append(report.Answers, PipelineAnswer{SampleID: s.ID, QuestionID: q.ID, Category: q.Category, Mode: mode, Query: q.Query, Expected: q.Answer, EvidenceRefs: q.Evidence, Error: "not executed"})
			}
		}
	}
	refreshPipelineSummaries(&report)
	if err = writePipelineJSON(filepath.Join(o.OutputDir, "pipeline.json"), report); err != nil {
		return report, err
	}
	answerIndex := 0
	var failures []error
	for _, s := range samples {
		owner := o.Prefix + "-" + s.ID
		var stages []PipelineStage
		var ingestErr error
		if pipelineNeedsIngestion(o.Modes) {
			stages, ingestErr = runtime.ingest(ctx, owner, s)
		}
		report.Stages = append(report.Stages, stages...)
		for _, stage := range stages {
			if stage.Error == "" {
				report.CompletedSessions++
			}
		}
		for _, q := range s.Questions {
			for _, mode := range o.Modes {
				result := PipelineAnswer{SampleID: s.ID, QuestionID: q.ID, Category: q.Category, Mode: mode, Query: q.Query, Expected: q.Answer, EvidenceRefs: q.Evidence}
				if ingestErr != nil && (mode == "derived" || mode == "combined") {
					result.Error = "ingestion incomplete: " + ingestErr.Error()
				} else {
					result = runtime.answer(ctx, owner, s, q, mode)
				}
				report.Answers[answerIndex] = result
				answerIndex++
				refreshPipelineSummaries(&report)
				if err = writePipelineJSON(filepath.Join(o.OutputDir, "pipeline.json"), report); err != nil {
					return report, err
				}
				if ctx.Err() != nil {
					return report, ctx.Err()
				}
			}
		}
		if ingestErr != nil {
			failures = append(failures, ingestErr)
		}
	}
	for _, a := range report.Answers {
		if a.Error != "" {
			failures = append(failures, fmt.Errorf("incomplete evaluation; inspect private pipeline.json"))
			break
		}
	}
	return report, errors.Join(failures...)
}

func summarizePipelineAnswers(answers []PipelineAnswer) map[string]PipelineModeSummary {
	out := map[string]PipelineModeSummary{}
	timings := map[string][]float64{}
	readyTimings := map[string][]float64{}
	for _, a := range answers {
		v := out[a.Mode]
		v.Expected++
		if v.RerankStatuses == nil {
			v.RerankStatuses = map[string]int{}
		}
		if a.Rerank.Status != "" {
			v.RerankStatuses[a.Rerank.Status]++
		}
		v.MeanAnswerInputTokens += float64(a.AnswerUsage.InputTokens)
		v.MeanPlannerInputTokens += float64(a.PlannerUsage.InputTokens)
		v.MeanPlannerOutputTokens += float64(a.PlannerUsage.OutputTokens)
		if a.Refinement.Status != "" {
			if v.RefinementStatuses == nil {
				v.RefinementStatuses = map[string]int{}
			}
			v.RefinementStatuses[a.Refinement.Status]++
		}
		if v.FollowupRerankStatuses == nil {
			v.FollowupRerankStatuses = map[string]int{}
		}
		for _, step := range a.Refinement.Steps {
			if step.Rerank.Status != "" {
				v.FollowupRerankStatuses[step.Rerank.Status]++
			}
		}
		if a.Judged && a.Error == "" {
			v.Judged++
		}
		if a.Correct && a.Judged && a.Error == "" {
			v.Correct++
		}
		if a.Error != "" {
			v.Errors++
		}
		v.MeanContextBytes += float64(a.ContextBytes)
		if a.SearchMillis > 0 {
			timings[a.Mode] = append(timings[a.Mode], a.SearchMillis)
			readyTimings[a.Mode] = append(readyTimings[a.Mode], a.SearchMillis+a.Refinement.Millis+a.EvidenceMillis)
		}
		out[a.Mode] = v
	}
	for mode, v := range out {
		v.Accuracy = float64(v.Correct) / float64(v.Expected)
		v.Coverage = float64(v.Judged) / float64(v.Expected)
		v.MeanContextBytes /= float64(v.Expected)
		v.MeanAnswerInputTokens /= float64(v.Expected)
		v.MeanPlannerInputTokens /= float64(v.Expected)
		v.MeanPlannerOutputTokens /= float64(v.Expected)
		sort.Float64s(readyTimings[mode])
		if n := len(readyTimings[mode]); n > 0 {
			v.EvidenceReadyP95Millis = readyTimings[mode][(95*n+99)/100-1]
		}
		sort.Float64s(timings[mode])
		if n := len(timings[mode]); n > 0 {
			v.SearchP95Millis = timings[mode][(95*n+99)/100-1]
		}
		out[mode] = v
	}
	return out
}
