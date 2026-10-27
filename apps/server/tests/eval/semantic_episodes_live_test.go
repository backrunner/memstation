package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/analysis"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/memoryllm"
	vectorstore "memstation/apps/server/internal/store/vector"
)

type (
	episodeProbeCase struct{ ID, Source, Question, Expected, Status string }
	episodeProbeCall struct {
		Task, InputSHA256, InstructionSHA256, Model, Error string
		Raw                                                json.RawMessage
		Usage                                              domain.UsageRecord
	}
)

type episodeProbeGenerator struct {
	delegate pipelinePlanGenerator
	calls    []episodeProbeCall
}

func (g *episodeProbeGenerator) Generate(ctx context.Context, req memoryllm.Request) (json.RawMessage, error) {
	raw, err := g.delegate.Generate(ctx, req)
	input, _ := json.Marshal(req.Input)
	call := episodeProbeCall{Task: req.Task, InputSHA256: pipelineHash(string(input)), InstructionSHA256: pipelineHash(req.Instruction), Model: g.delegate.model, Raw: append(json.RawMessage{}, raw...), Usage: g.delegate.usage}
	if err != nil {
		call.Error = "model call failed"
	}
	g.calls = append(g.calls, call)
	return raw, err
}

type episodeProbeResult struct {
	Case                episodeProbeCase
	ExtractionError     string
	ModelCalls          []episodeProbeCall
	Documents           []domain.MemoryDocument
	StatusMatched       bool
	QA                  PipelineAnswer
	AnswerRaw, JudgeRaw json.RawMessage
}

// A source-only diagnostic of an explicitly selected external session. No QA or
// gold references are sent to extraction. A fresh artifact records every attempt;
// this is not a complete LOCOMO run or a quality score.
func TestLiveSemanticEpisodeLongSession(t *testing.T) {
	out := os.Getenv("MEMSTATION_EVAL_EPISODE_SESSION_OUT")
	if out == "" {
		t.Skip("manual real-model long-session episode diagnostic")
	}
	revision := os.Getenv("MEMSTATION_EVAL_CODE_REVISION")
	if revision == "" {
		t.Fatal("explicit code revision required")
	}
	samples, err := loadPipelineSamples(os.Getenv("MEMSTATION_EVAL_LOCOMO_PATH"), os.Getenv("MEMSTATION_EVAL_SAMPLE"), 1)
	if err != nil || len(samples) != 1 {
		t.Fatal("select exactly one external sample", err)
	}
	var record domain.ConversationRecord
	for _, session := range samples[0].Sessions {
		if session.ID == os.Getenv("MEMSTATION_EVAL_SESSION") {
			record = domain.ConversationRecord{UserID: "long-session-probe", ConversationID: session.ID, Messages: session.Messages, Revision: 1, AgentID: "fixture-agent"}
		}
	}
	if record.ConversationID == "" {
		t.Fatal("selected session not found")
	}
	if err = os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, err := normalizeLongMemEvalQAJudgeOptions(LongMemEvalQAJudgeOptions{ReasoningEffort: os.Getenv("EXTRACTION_REASONING_EFFORT"), Model: os.Getenv("EXTRACTION_MODEL"), BaseURL: os.Getenv("EXTRACTION_BASE_URL"), APIKey: os.Getenv("EXTRACTION_API_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	g := &episodeProbeGenerator{delegate: pipelinePlanGenerator{config: cfg}}
	svc := analysis.NewServiceWithOptions(g, analysis.Options{SemanticEpisodes: true})
	in := analysis.Input{UserID: record.UserID, ConversationID: record.ConversationID, Conversation: record}
	// Optional frozen facts reproduce the long context preceding a failed
	// session. They remain context, never evidence. Do not load prior answers.
	parent := os.Getenv("MEMSTATION_EVAL_EPISODE_CONTEXT_REPORT")
	parentHash := ""
	if parent != "" {
		raw, e := os.ReadFile(parent)
		if e != nil {
			t.Fatal(e)
		}
		var prior LOCOMOPipelineReport
		if e = json.Unmarshal(raw, &prior); e != nil {
			t.Fatal(e)
		}
		parentHash = pipelineHash(string(raw))
		for _, stage := range prior.Stages {
			if stage.SessionID == record.ConversationID {
				break
			}
			for _, d := range stage.Documents {
				if d.Type == domain.MemoryTypeFact {
					d.UserID = record.UserID
					in.ExistingFacts = append(in.ExistingFacts, d)
				}
			}
		}
	}
	manifest := map[string]any{"scope": "source-only long-session diagnostic; not LOCOMO QA", "sample_id": samples[0].ID, "session_id": record.ConversationID, "turns": len(record.Messages), "code_revision": revision, "model": cfg.runInfo(), "source_sha256": pipelineHash(mustProbeJSON(analysis.Sources(record))), "semantic_episode_policy_sha256": analysis.SemanticEpisodePolicyHash(), "parent_context_report_sha256": parentHash, "existing_facts": len(in.ExistingFacts)}
	var frozenSummary *analysis.Summary
	if path := os.Getenv("MEMSTATION_EVAL_EPISODE_FROZEN_SUMMARY"); path != "" {
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		var prior struct {
			Calls []episodeProbeCall `json:"calls"`
		}
		if e = json.Unmarshal(raw, &prior); e != nil || len(prior.Calls) == 0 || prior.Calls[0].Task != "summary.generate" || prior.Calls[0].Error != "" {
			t.Fatal("invalid frozen summary parent", e)
		}
		var summary analysis.Summary
		if e = memoryllm.Decode(prior.Calls[0].Raw, &summary); e != nil {
			t.Fatal(e)
		}
		frozenSummary = &summary
		manifest["frozen_summary_parent_sha256"] = pipelineHash(string(raw))
		manifest["summary_cost_historical_not_remeasured"] = true
	}
	save := func(result analysis.Result, failure string, seconds float64) {
		t.Helper()
		if e := writePipelineJSON(filepath.Join(out, "report.json"), map[string]any{"manifest": manifest, "calls": g.calls, "result": result, "error": failure, "seconds": seconds}); e != nil {
			t.Fatal(e)
		}
	}
	save(analysis.Result{}, "not completed", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	start := time.Now()
	var summary analysis.Summary
	if frozenSummary == nil {
		summary, err = svc.SummarizeConversation(ctx, in)
	} else {
		summary = *frozenSummary
	}
	var result analysis.Result
	if err == nil {
		in.Summary = domain.MemoryDocument{Content: summary.Summary}
		result, err = svc.AnalyzeConversation(ctx, in)
	}
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	save(result, failure, time.Since(start).Seconds())
	if err != nil {
		t.Fatal(err)
	}
}

// Explicit manual probe with repository-owned synthetic sources. The model only
// sees sources during extraction; expected answers are used solely by the judge.
// Not a CI benchmark or a LOCOMO score. Output directory must be fresh.
func TestLiveSemanticEpisodeExtractionAndRecall(t *testing.T) {
	out := os.Getenv("MEMSTATION_EVAL_EPISODE_OUT")
	if out == "" {
		t.Skip("manual real-model episode probe")
	}
	replaySource := os.Getenv("MEMSTATION_EVAL_EPISODE_REPLAY_SOURCE")
	allDerived := os.Getenv("MEMSTATION_EVAL_EPISODE_ALL_DERIVED") == "true"
	var frozen struct {
		Results []episodeProbeResult `json:"results"`
	}
	if replaySource != "" {
		raw, e := os.ReadFile(replaySource)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(raw, &frozen); e != nil {
			t.Fatal(e)
		}
	}
	revision := os.Getenv("MEMSTATION_EVAL_CODE_REVISION")
	if revision == "" {
		t.Fatal("explicit code revision required")
	}
	config := func(role string) LongMemEvalQAJudgeOptions {
		t.Helper()
		cfg, err := normalizeLongMemEvalQAJudgeOptions(LongMemEvalQAJudgeOptions{ReasoningEffort: os.Getenv(role + "_REASONING_EFFORT"), Model: os.Getenv(role + "_MODEL"), BaseURL: os.Getenv(role + "_BASE_URL"), APIKey: os.Getenv(role + "_API_KEY")})
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	extraction, reader, judge := config("EXTRACTION"), config("ANSWERER"), config("JUDGE")
	embedding, err := normalizeEmbeddingOptions(EmbeddingOptions{Mode: EmbeddingModeProvider, Model: os.Getenv("EMBEDDING_MODEL"), BaseURL: os.Getenv("EMBEDDING_BASE_URL"), APIKey: os.Getenv("EMBEDDING_API_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	client, err := newEvalEmbeddingClient(embedding)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []episodeProbeCase{
		{ID: "cancelled", Source: "Alice: I had planned a museum visit for Friday. Alice: The museum announced it would be closed, so I cancelled the visit. I did not go to the museum.", Question: "Did Alice's planned museum visit take place?", Expected: "No. Alice cancelled the visit because the museum would be closed.", Status: "cancelled"},
		{ID: "planned", Source: "Ben: Next month I plan to enroll in a pottery course. I have not enrolled yet and the course has not started.", Question: "Has Ben already enrolled in and started the pottery course?", Expected: "No. Ben plans to enroll next month; he has not enrolled and the course has not started.", Status: "planned"},
		{ID: "completed", Source: "Mei: Yesterday Nora completed the first-aid course and received her certificate. I did not attend that course; Nora is the person who completed it.", Question: "Who completed the first-aid course, and did Mei attend?", Expected: "Nora completed the course and received her certificate. Mei did not attend.", Status: "completed"},
		{ID: "changed", Source: "Ravi: I planned to volunteer at the animal shelter on Saturday. Ravi: The shelter asked me to switch to Sunday. I volunteered there on Sunday and did not volunteer there on Saturday.", Question: "On which day did Ravi actually volunteer at the animal shelter?", Expected: "Sunday, after switching from the original Saturday plan.", Status: "completed"},
		{ID: "negated", Source: "Lena: Omar and I finished a beach cleanup last weekend. Lena: We collected plastic waste, but neither Omar nor I took part in the separate tree-planting event.", Question: "What activity did Lena and Omar complete together, and did they join the tree-planting event?", Expected: "They completed a beach cleanup and collected plastic waste. Neither joined the tree-planting event.", Status: "completed"},
	}
	repo := memorystore.NewInMemoryMemoryRepository()
	vectors := vectorstore.NewPGVectorStore(nil)
	vectors.SetEmbedder(client)
	engine := memoryservice.NewEngine(repo, vectors)
	results := make([]episodeProbeResult, len(cases))
	for i, c := range cases {
		results[i] = episodeProbeResult{Case: c, ExtractionError: "not executed", QA: PipelineAnswer{SampleID: "native-episodes", QuestionID: c.ID, Mode: "episodes", Query: c.Question, Expected: c.Expected, Error: "not executed"}}
	}
	if replaySource != "" && len(frozen.Results) != len(cases) {
		t.Fatal("incomplete frozen extraction report")
	}
	manifest := map[string]any{"code_revision": revision, "scope": "synthetic functional probe; not LOCOMO quality", "extraction_model": extraction.Model, "reader_model": reader.Model, "judge_model": judge.Model, "embedding": embedding.runInfo(), "rerank": "disabled", "top_n": 3, "context_bytes": 16000, "judge_policy": "alternatives_v3", "cases_sha256": pipelineHash(mustProbeJSON(cases))}
	manifest["reasoning_effort"] = map[string]string{"extraction": extraction.ReasoningEffort, "reader": reader.ReasoningEffort, "judge": judge.ReasoningEffort}
	if allDerived {
		manifest["retrieval_types"] = "all derived"
	} else {
		manifest["retrieval_types"] = "episode only"
	}
	if replaySource != "" {
		raw, _ := os.ReadFile(replaySource)
		manifest["parent_extraction_sha256"] = pipelineHash(string(raw))
		manifest["extraction_costs_frozen_not_remeasured"] = true
	}
	save := func() {
		t.Helper()
		if e := writePipelineJSON(filepath.Join(out, "report.json"), map[string]any{"manifest": manifest, "results": results}); e != nil {
			t.Fatal(e)
		}
	}
	save()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	for i, c := range cases {
		r := &results[i]
		r.ExtractionError = "attempt interrupted"
		save()
		g := &episodeProbeGenerator{delegate: pipelinePlanGenerator{config: extraction}}
		var generator memoryllm.Generator = g
		if replaySource != "" {
			if frozen.Results[i].Case != c || frozen.Results[i].ExtractionError != "" {
				t.Fatal("frozen source identity/error mismatch")
			}
			generator = &episodeFrozenGenerator{calls: frozen.Results[i].ModelCalls}
		}
		svc := analysis.NewServiceWithOptions(generator, analysis.Options{SemanticEpisodes: true})
		input := analysis.Input{UserID: "episode-probe", ConversationID: c.ID, Conversation: domain.ConversationRecord{UserID: "episode-probe", ConversationID: c.ID, Revision: 1, Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: c.Source}}, AgentID: "fixture-agent"}}
		generated, e := svc.AnalyzeConversation(ctx, input)
		r.ModelCalls = g.calls
		if replaySource != "" {
			r.ModelCalls = frozen.Results[i].ModelCalls
		}
		if e != nil {
			r.ExtractionError = e.Error()
			save()
			t.Errorf("%s extraction: %v", c.ID, e)
			continue
		}
		r.ExtractionError = ""
		documents := append([]domain.MemoryDocument{}, generated.GenericItems...)
		if allDerived {
			documents = append(documents, generated.Facts...)
			if generated.Impression.ID != "" {
				documents = append(documents, generated.Impression)
			}
		}
		for _, d := range documents {
			if !allDerived && d.Type != domain.MemoryTypeEpisode {
				continue
			}
			d.CreatedAt = time.Now().UTC()
			d.UpdatedAt = d.CreatedAt
			if e = repo.StoreDocument(ctx, d); e != nil {
				t.Fatal(e)
			}
			d, e = repo.GetDocument(ctx, d.UserID, d.ID, d.Type)
			if e != nil {
				t.Fatal(e)
			}
			if e = vectors.Upsert(ctx, domain.VectorUpsertRequest{UserID: d.UserID, GenerationID: d.GenerationID, NodeID: d.NodeID, NodeType: string(d.Type), Content: d.Content, EmbeddingVersion: 1, EmbeddingModel: embedding.Model}); e != nil {
				t.Fatal(e)
			}
			r.Documents = append(r.Documents, d)
			if d.Metadata["status"] == c.Status {
				r.StatusMatched = true
			}
		}
		save()
	}
	for i := range results {
		r := &results[i]
		a := &r.QA
		if r.ExtractionError != "" {
			a.Error = "extraction failed"
			save()
			continue
		}
		a.Error = "attempt interrupted"
		save()
		types := []domain.MemoryType{domain.MemoryTypeEpisode}
		if allDerived {
			types = domain.DefaultSearchMemoryTypes(false)
		}
		docs, e := engine.Search(ctx, domain.SearchInput{UserID: "episode-probe", Query: a.Query, TopN: 3, MemoryTypes: types})
		if e != nil {
			a.Error = "retrieval failed"
			save()
			t.Error(e)
			continue
		}
		packet, e := engine.PrepareEvidence(ctx, domain.SearchInput{UserID: "episode-probe"}, docs.Memories, memoryservice.EvidenceOptions{MaxBytes: 16000})
		if e != nil {
			t.Fatal(e)
		}
		a.Context = packet.Text
		a.ContextHash = pipelineHash(a.Context)
		a.ContextBytes = len(a.Context)
		a.ContextDocuments = len(packet.Memories)
		raw, usage, model, e := pipelineCompleteObserved(ctx, reader, pipelineCoverageInstruction, map[string]any{"question": a.Query, "memories": a.Context})
		r.AnswerRaw = raw
		a.AnswerUsage = usage
		a.AnswerModel = model
		var answer struct {
			Answer string `json:"answer"`
		}
		if e != nil || memoryllm.Decode(raw, &answer) != nil || answer.Answer == "" {
			a.Error = "invalid reader response"
			save()
			t.Errorf("%s reader", a.QuestionID)
			continue
		}
		a.Answer = answer.Answer
		raw, usage, model, e = pipelineCompleteObserved(ctx, judge, pipelineAlternativeJudgeInstruction, map[string]any{"question": a.Query, "reference_answer": a.Expected, "candidate_answer": a.Answer})
		r.JudgeRaw = raw
		a.JudgeUsage = usage
		a.JudgeModel = model
		var grade struct {
			Correct *bool  `json:"correct"`
			Reason  string `json:"reason"`
		}
		if e != nil || memoryllm.Decode(raw, &grade) != nil || grade.Correct == nil {
			a.Error = "invalid judge response"
			save()
			t.Errorf("%s judge", a.QuestionID)
			continue
		}
		a.Correct = *grade.Correct
		a.Judged = true
		a.JudgeReason = grade.Reason
		a.Error = ""
		save()
	}
}

func mustProbeJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }

type episodeFrozenGenerator struct {
	calls []episodeProbeCall
	index int
}

func (g *episodeFrozenGenerator) Generate(_ context.Context, req memoryllm.Request) (json.RawMessage, error) {
	if g.index >= len(g.calls) {
		return nil, fmt.Errorf("frozen model responses exhausted")
	}
	call := g.calls[g.index]
	g.index++
	input, _ := json.Marshal(req.Input)
	if call.InputSHA256 != pipelineHash(string(input)) || call.InstructionSHA256 != pipelineHash(req.Instruction) || call.Error != "" {
		return nil, fmt.Errorf("frozen extraction request drift")
	}
	return append(json.RawMessage{}, call.Raw...), nil
}
