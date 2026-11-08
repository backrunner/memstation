package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
	"memstation/apps/server/internal/service/memoryllm"
)

type packetReplayCase struct {
	Cohort    string                  `json:"cohort"`
	Group     string                  `json:"group"`
	Baseline  PipelineAnswer          `json:"baseline"`
	Documents []domain.MemoryDocument `json:"documents"`
}

type packetReplayPlan struct {
	ReaderPolicy       string             `json:"reader_policy"`
	ContextBytes       int                `json:"context_bytes"`
	ReaderOutputTokens int                `json:"reader_output_tokens"`
	JudgeOutputTokens  int                `json:"judge_output_tokens"`
	MaxGenerationCalls int                `json:"max_generation_calls"`
	TokenStopLimit     int                `json:"token_stop_limit"`
	Cases              []packetReplayCase `json:"cases"`
	// CandidateReasoningEffort compares reader effort on identical evidence and prompt.
	// Only the reader changes; the judge keeps its frozen settings.
	CandidateReasoningEffort string `json:"candidate_reasoning_effort,omitempty"`
	// CandidateOnly reuses the frozen baseline row as the control instead of paying
	// for a fresh baseline call on every case.
	CandidateOnly bool `json:"candidate_only,omitempty"`
}

type preparedPacketReplay struct {
	PlanSHA256       string                         `json:"plan_sha256"`
	Packets          []memoryservice.EvidencePacket `json:"packets"`
	Preparation      map[string]string              `json:"preparation"`
	ReaderComparison *frozenReaderComparison        `json:"reader_comparison,omitempty"`
}

// A reader experiment compares a frozen prior instruction to the current code.
// It must not simultaneously change the context or masquerade as retrieval gain.
type frozenReaderComparison struct {
	BaselineInstruction  string `json:"baseline_instruction"`
	BaselineSHA256       string `json:"baseline_sha256"`
	CandidateInstruction string `json:"candidate_instruction"`
	CandidateSHA256      string `json:"candidate_sha256"`
}

func packetReplayInstructions(plan packetReplayPlan, prepared preparedPacketReplay) (map[string]string, error) {
	current := pipelineReaderInstruction(plan.ReaderPolicy)
	prompts := map[string]string{"baseline": current, "candidate": current}
	if prepared.Preparation["kind"] != "frozen_reader_comparison" {
		if prepared.ReaderComparison != nil {
			return nil, fmt.Errorf("reader comparison requires explicit reader-only provenance")
		}
		return prompts, nil
	}
	r := prepared.ReaderComparison
	if r == nil || strings.TrimSpace(r.BaselineInstruction) == "" || r.BaselineSHA256 != pipelineHash(r.BaselineInstruction) || r.CandidateSHA256 != pipelineHash(r.CandidateInstruction) || r.CandidateInstruction != current || (r.BaselineInstruction == r.CandidateInstruction && plan.CandidateReasoningEffort == "") || len(prepared.Packets) != len(plan.Cases) {
		return nil, fmt.Errorf("invalid frozen reader instructions")
	}
	for i, packet := range prepared.Packets {
		row := plan.Cases[i]
		if packet.Text != row.Baseline.Context || !reflect.DeepEqual(packet.Memories, row.Documents) || !reflect.DeepEqual(packet.SourceIDs, row.Baseline.ContextSourceIDs) {
			return nil, fmt.Errorf("reader comparison changed frozen evidence")
		}
	}
	prompts["baseline"] = r.BaselineInstruction
	return prompts, nil
}

func packetReaderPayload(answer PipelineAnswer, empty bool) map[string]any {
	contextText := answer.Context
	if empty {
		contextText = ""
	}
	return map[string]any{"question": answer.Query, "memories": contextText}
}

func loadPacketReplayPlan(t *testing.T) (packetReplayPlan, string, string) {
	t.Helper()
	path := os.Getenv("MEMSTATION_EVAL_PACKET_REPLAY_PLAN")
	if path == "" {
		t.Skip("manual failure-focused frozen-packet diagnostic")
	}
	raw, err := os.ReadFile(path)
	var plan packetReplayPlan
	if err != nil || memoryllm.Decode(raw, &plan) != nil {
		t.Fatal("invalid bounded packet replay plan", err)
	}
	if err := validatePacketReplayPlan(plan); err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("MEMSTATION_EVAL_PACKET_REPLAY_OUT")
	if out == "" {
		t.Fatal("packet replay output is required")
	}
	return plan, pipelineHash(string(raw)), out
}

func validatePacketReplayPlan(plan packetReplayPlan) error {
	if len(plan.Cases) == 0 || len(plan.Cases) > 64 || (plan.ReaderPolicy != "interpretation" && plan.ReaderPolicy != "reasoning" && plan.ReaderPolicy != "inference_reasoning" && plan.ReaderPolicy != "direct") || plan.ContextBytes < 1 || plan.ContextBytes > 32000 || plan.ReaderOutputTokens < 1 || plan.JudgeOutputTokens != 4096 || plan.MaxGenerationCalls != len(plan.Cases)*4 || plan.TokenStopLimit < 1 {
		return fmt.Errorf("invalid packet replay budgets")
	}
	seen := map[string]bool{}
	for _, row := range plan.Cases {
		a := row.Baseline
		key := a.SampleID + "|" + a.QuestionID + "|" + a.Mode
		if seen[key] || a.Error != "" || a.ContextHash != pipelineHash(a.Context) || a.ContextBytes != len(a.Context) || a.ContextBytes > plan.ContextBytes || len(row.Documents) == 0 || len(row.Documents) != a.ContextDocuments || row.Group != "failure" && row.Group != "control" {
			return fmt.Errorf("invalid frozen case: %s", key)
		}
		seen[key] = true
	}
	return nil
}

func TestPacketReplayRejectsInvalidBudgetsAndFrozenEvidence(t *testing.T) {
	fixture := func() packetReplayPlan {
		return packetReplayPlan{ReaderPolicy: "interpretation", ContextBytes: 14000, ReaderOutputTokens: 16384, JudgeOutputTokens: 4096, MaxGenerationCalls: 4, TokenStopLimit: 50000, Cases: []packetReplayCase{{Group: "failure", Baseline: PipelineAnswer{QuestionID: "case", Context: "source", ContextHash: pipelineHash("source"), ContextBytes: 6, ContextDocuments: 1}, Documents: []domain.MemoryDocument{{ID: "record", Content: "source"}}}}}
	}
	if err := validatePacketReplayPlan(fixture()); err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{24000, 32000} {
		plan := fixture()
		plan.ContextBytes = budget
		if err := validatePacketReplayPlan(plan); err != nil {
			t.Fatal("declared quality experiment budget rejected", err)
		}
	}
	for _, test := range []struct {
		name   string
		change func(*packetReplayPlan)
	}{
		{"missing_token_limit", func(p *packetReplayPlan) { p.TokenStopLimit = 0 }},
		{"wrong_call_count", func(p *packetReplayPlan) { p.MaxGenerationCalls++ }},
		{"duplicate_case", func(p *packetReplayPlan) {
			p.Cases = append(p.Cases, p.Cases[0])
			p.MaxGenerationCalls = 8
		}},
		{"changed_context", func(p *packetReplayPlan) { p.Cases[0].Baseline.Context = "edited" }},
		{"over_budget", func(p *packetReplayPlan) { p.ContextBytes = 5 }},
		{"unbounded_context", func(p *packetReplayPlan) { p.ContextBytes = 32001 }},
		{"empty_evidence", func(p *packetReplayPlan) {
			p.Cases[0].Documents = nil
			p.Cases[0].Baseline.ContextDocuments = 0
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := fixture()
			test.change(&plan)
			if validatePacketReplayPlan(plan) == nil {
				t.Fatal("invalid plan could reach model calls")
			}
		})
	}
}

// Check the full records at preparation and again before inference. Matching
// IDs and content alone would miss changed scope, attribution or source revision.
func validateSelectedRecordPacket(row packetReplayCase, packet memoryservice.EvidencePacket) error {
	if !reflect.DeepEqual(packet.Memories, row.Documents) || !reflect.DeepEqual(packet.SourceIDs, row.Baseline.ContextSourceIDs) {
		return fmt.Errorf("presentation replay changed canonical records or source bindings")
	}
	if len(packet.SourceIDs) != len(packet.Memories) {
		return fmt.Errorf("presentation replay has incomplete source bindings")
	}
	for i, doc := range packet.Memories {
		if doc.ID == "" || packet.SourceIDs[fmt.Sprintf("memory-%d", i+1)] != doc.ID {
			return fmt.Errorf("presentation replay source binding does not identify its record")
		}
	}
	if strings.TrimSpace(packet.Text) == "" || len(packet.Text) > row.Baseline.ContextBytes {
		return fmt.Errorf("presentation replay has empty or increased context")
	}
	return nil
}

func TestPresentationReplayRejectsRecordAndProvenanceChanges(t *testing.T) {
	fixture := func() (packetReplayCase, memoryservice.EvidencePacket) {
		doc := domain.MemoryDocument{ID: "record", UserID: "owner", ConversationID: "conversation", Revision: 2, Type: domain.MemoryTypeFact, Content: "A source statement.", Metadata: map[string]any{"speaker": "Mira"}, SourceRefs: []domain.SourceRef{{WorkspaceID: "workspace", MessageID: "message", Revision: 3}}}
		row := packetReplayCase{Documents: []domain.MemoryDocument{doc}, Baseline: PipelineAnswer{ContextBytes: 200, ContextSourceIDs: map[string]string{"memory-1": "record"}}}
		copy := doc
		copy.Metadata = map[string]any{"speaker": "Mira"}
		copy.SourceRefs = append([]domain.SourceRef(nil), doc.SourceRefs...)
		return row, memoryservice.EvidencePacket{Text: "Changed presentation.", Memories: []domain.MemoryDocument{copy}, SourceIDs: map[string]string{"memory-1": "record"}}
	}
	row, packet := fixture()
	if err := validateSelectedRecordPacket(row, packet); err != nil {
		t.Fatal("valid presentation rejected", err)
	}
	for name, mutate := range map[string]func(*memoryservice.EvidencePacket){
		"owner":           func(p *memoryservice.EvidencePacket) { p.Memories[0].UserID = "other" },
		"conversation":    func(p *memoryservice.EvidencePacket) { p.Memories[0].ConversationID = "other" },
		"revision":        func(p *memoryservice.EvidencePacket) { p.Memories[0].Revision++ },
		"type":            func(p *memoryservice.EvidencePacket) { p.Memories[0].Type = domain.MemoryTypeTask },
		"attribution":     func(p *memoryservice.EvidencePacket) { p.Memories[0].Metadata["speaker"] = "Other" },
		"source_revision": func(p *memoryservice.EvidencePacket) { p.Memories[0].SourceRefs[0].Revision++ },
		"content":         func(p *memoryservice.EvidencePacket) { p.Memories[0].Content += " Unsupported." },
		"lost_record":     func(p *memoryservice.EvidencePacket) { p.Memories = nil },
		"added_record":    func(p *memoryservice.EvidencePacket) { p.Memories = append(p.Memories, p.Memories[0]) },
		"source_binding":  func(p *memoryservice.EvidencePacket) { p.SourceIDs["memory-1"] = "other" },
		"added_binding":   func(p *memoryservice.EvidencePacket) { p.SourceIDs["memory-2"] = "other" },
		"empty_context":   func(p *memoryservice.EvidencePacket) { p.Text = " " },
		"context_growth":  func(p *memoryservice.EvidencePacket) { p.Text = strings.Repeat("x", 201) },
	} {
		t.Run(name, func(t *testing.T) {
			row, packet := fixture()
			mutate(&packet)
			if validateSelectedRecordPacket(row, packet) == nil {
				t.Fatal("presentation replay accepted a change beyond rendering")
			}
		})
	}
	for _, ids := range []map[string]string{nil, {"memory-1": "other"}} {
		row, packet := fixture()
		row.Baseline.ContextSourceIDs, packet.SourceIDs = ids, ids
		if validateSelectedRecordPacket(row, packet) == nil {
			t.Fatal("matching but invalid frozen source bindings accepted")
		}
	}
}

// Preparation invokes the production packet builder with only the already
// selected documents. Nil readers/stores make new retrieval or hydration
// impossible. Every document must survive, under the original byte ceiling.
func TestPrepareLOCOMOPacketReplay(t *testing.T) {
	plan, hash, out := loadPacketReplayPlan(t)
	prepared := preparedPacketReplay{PlanSHA256: hash, Preparation: map[string]string{"kind": "same_selected_records"}}
	engine := memoryservice.NewEngine(nil, nil)
	for _, row := range plan.Cases {
		first := row.Documents[0]
		packet, err := engine.PrepareEvidence(context.Background(), domain.SearchInput{UserID: first.UserID, WorkspaceID: first.WorkspaceID}, row.Documents, memoryservice.EvidenceOptions{MaxBytes: plan.ContextBytes})
		if err != nil {
			t.Fatal("packet preparation failed", row.Baseline.QuestionID, err)
		}
		if err := validateSelectedRecordPacket(row, packet); err != nil {
			t.Fatal(row.Baseline.QuestionID, err)
		}
		prepared.Packets = append(prepared.Packets, packet)
	}
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal("prepare requires a fresh directory", err)
	}
	if err := writePipelineJSON(filepath.Join(out, "packets.json"), prepared); err != nil {
		t.Fatal(err)
	}
}

type packetReplayResult struct {
	Arm    string         `json:"arm"`
	Cohort string         `json:"cohort"`
	Group  string         `json:"group"`
	Answer PipelineAnswer `json:"answer"`
}

// This replay is a diagnostic for selected development failures, not an
// estimate of benchmark accuracy. Historical scores choose the offline cohort
// only. Reader inputs contain just the question and authorized frozen evidence.
func TestLiveLOCOMOPacketReplay(t *testing.T) {
	runLiveLOCOMOPacketReplay(t, false)
}

// Empty-memory replies are a separate grounding diagnostic, not answers to be
// graded against the nonempty-memory references. They never invoke the judge.
func TestLiveLOCOMOEmptyReaderReplay(t *testing.T) {
	runLiveLOCOMOPacketReplay(t, true)
}

func runLiveLOCOMOPacketReplay(t *testing.T, empty bool) {
	t.Helper()
	plan, hash, out := loadPacketReplayPlan(t)
	preparedRaw, err := os.ReadFile(os.Getenv("MEMSTATION_EVAL_PACKET_REPLAY_PREPARED"))
	var prepared preparedPacketReplay
	if err != nil || memoryllm.Decode(preparedRaw, &prepared) != nil || prepared.PlanSHA256 != hash || len(prepared.Packets) != len(plan.Cases) || pipelineHash(string(preparedRaw)) != os.Getenv("MEMSTATION_EVAL_PACKET_REPLAY_PREPARED_SHA256") {
		t.Fatal("prepared packet provenance mismatch", err)
	}
	if prepared.Preparation["kind"] != "same_selected_records" && prepared.Preparation["kind"] != "frozen_query_provider_retrieval" && prepared.Preparation["kind"] != "frozen_source_support" && prepared.Preparation["kind"] != "frozen_reader_comparison" {
		t.Fatal("explicit packet preparation provenance required")
	}
	instructions, err := packetReplayInstructions(plan, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if empty && prepared.ReaderComparison == nil {
		t.Fatal("empty-memory reader comparison requires frozen instructions")
	}
	if prepared.Preparation["kind"] == "same_selected_records" {
		for i, row := range plan.Cases {
			if err := validateSelectedRecordPacket(row, prepared.Packets[i]); err != nil {
				t.Fatal("invalid presentation packet before inference", row.Baseline.QuestionID, err)
			}
		}
	}
	reader, judge := pipelineReplayRoleConfig(t, "ANSWERER"), pipelineReplayRoleConfig(t, "JUDGE")
	if reader.Model == "" || judge.Model == "" || reader.outputTokenLimit() != plan.ReaderOutputTokens || judge.outputTokenLimit() != plan.JudgeOutputTokens {
		t.Fatal("initial output budgets must equal configured ceilings: no generation retry")
	}
	revision := os.Getenv("MEMSTATION_EVAL_CODE_REVISION")
	if revision == "" {
		t.Fatal("source revision is required")
	}
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal("a new attempt requires a fresh output directory", err)
	}
	var results []packetReplayResult
	for i, row := range plan.Cases {
		arms := []string{"baseline", "candidate"}
		if plan.CandidateOnly {
			arms = []string{"candidate"}
		}
		if i%2 != 0 && !plan.CandidateOnly {
			arms[0], arms[1] = arms[1], arms[0]
		}
		for _, arm := range arms {
			b := row.Baseline
			text := b.Context
			documents := b.ContextDocuments
			if arm == "candidate" {
				text = prepared.Packets[i].Text
				documents = len(prepared.Packets[i].Memories)
			}
			if empty {
				text, documents = "", 0
			}
			if len(text) > plan.ContextBytes {
				t.Fatal("prepared context exceeds frozen budget")
			}
			results = append(results, packetReplayResult{Arm: arm, Cohort: row.Cohort, Group: row.Group, Answer: PipelineAnswer{SampleID: b.SampleID, QuestionID: b.QuestionID, Category: b.Category, Mode: b.Mode, Query: b.Query, Expected: b.Expected, Context: text, ContextHash: pipelineHash(text), ContextBytes: len(text), ContextDocuments: documents, DiagnosticOnly: true, Error: "not executed"}})
		}
	}
	manifest := map[string]any{"code_revision": revision, "plan_sha256": hash, "prepared_sha256": pipelineHash(string(preparedRaw)), "preparation": prepared.Preparation, "reader": reader.runInfo(), "judge": judge.runInfo(), "reader_prompt_sha256": pipelineHash(pipelineReaderInstruction(plan.ReaderPolicy)), "judge_prompt_sha256": pipelineHash(pipelineJudgeInstruction), "generation_call_limit": plan.MaxGenerationCalls, "token_stop_limit": plan.TokenStopLimit, "scope": "failure-focused development diagnostic; paired precomputed contexts; generation phase performs no ingestion or retrieval; no whole-benchmark claim", "order": "alternate baseline/candidate first per frozen case"}
	manifest["reader_prompts_sha256"] = map[string]string{"baseline": pipelineHash(instructions["baseline"]), "candidate": pipelineHash(instructions["candidate"])}
	manifest["empty_memory"] = empty
	manifest["candidate_reasoning_effort"] = plan.CandidateReasoningEffort
	manifest["candidate_only"] = plan.CandidateOnly
	if prepared.ReaderComparison != nil {
		manifest["scope"] = "reader-only development diagnostic; identical frozen evidence; no retrieval improvement or replacement eligibility"
	}
	if empty {
		manifest["scope"] = "empty-memory grounding diagnostic; no judge or benchmark accuracy"
		manifest["generation_call_limit"] = len(plan.Cases) * 2
	}
	calls, tokens := 0, 0
	save := func() {
		t.Helper()
		if err := writePipelineJSON(filepath.Join(out, "results.json"), map[string]any{"manifest": manifest, "generation_calls": calls, "reported_tokens": tokens, "results": results}); err != nil {
			t.Fatal(err)
		}
	}
	// Reserve output capacity and a conservative byte-based input allowance
	// before every call. If exhausted, remaining rows retain failure status.
	reserve := func(prompt string, payload any, output int) bool {
		raw, err := json.Marshal(payload)
		return err == nil && calls < plan.MaxGenerationCalls && tokens+len(raw)+len(prompt)+output+1024 <= plan.TokenStopLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	save()
	for i := range results {
		a := &results[i].Answer
		prompt := instructions[results[i].Arm]
		payload := packetReaderPayload(*a, empty)
		if ctx.Err() != nil || !reserve(prompt, payload, plan.ReaderOutputTokens) {
			t.Fatal("time or cost budget exhausted; unexecuted rows stay in denominator")
		}
		a.Error = "reader attempt interrupted"
		calls++
		save()
		start := time.Now()
		armReader := reader
		if results[i].Arm == "candidate" && plan.CandidateReasoningEffort != "" {
			armReader.ReasoningEffort = plan.CandidateReasoningEffort
		}
		a.RawAnswer, a.AnswerUsage, a.AnswerModel, err = pipelineReadObserved(ctx, armReader, prompt, payload, plan.ReaderOutputTokens)
		a.Answer = a.RawAnswer
		if err == nil && results[i].Arm == "candidate" {
			a.Answer, err = pipelineFinalAnswer(plan.ReaderPolicy, a.RawAnswer)
		}
		a.AnswerMillis = float64(time.Since(start).Microseconds()) / 1000
		tokens += a.AnswerUsage.TotalTokens
		if a.AnswerUsage.TotalTokens <= 0 || a.AnswerUsage.TotalTokens != a.AnswerUsage.InputTokens+a.AnswerUsage.OutputTokens || tokens > plan.TokenStopLimit {
			a.Error = "reader usage unavailable, inconsistent or over budget"
			save()
			t.Fatal(a.Error)
		}
		if err != nil {
			a.Error = err.Error()
			save()
			t.Errorf("reader failed: %s %s", a.QuestionID, results[i].Arm)
			continue
		}
		if empty {
			a.Error = ""
			save()
			t.Logf("empty memory completed %d/%d calls=%d tokens=%d", i+1, len(results), calls, tokens)
			continue
		}
		gradePayload := map[string]any{"question": a.Query, "reference_answer": a.Expected, "candidate_answer": a.Answer}
		a.Error = "judge not executed"
		save()
		if !reserve(pipelineJudgeInstruction, gradePayload, plan.JudgeOutputTokens) {
			t.Fatal("cost budget exhausted before judge; all rows stay in denominator")
		}
		a.Error = "judge attempt interrupted"
		calls++
		save()
		start = time.Now()
		raw, usage, model, gradeErr := pipelineCompleteObservedBudget(ctx, judge, pipelineJudgeInstruction, gradePayload, plan.JudgeOutputTokens)
		a.RawJudgment, a.JudgeUsage, a.JudgeModel = string(raw), usage, model
		a.JudgeMillis = float64(time.Since(start).Microseconds()) / 1000
		tokens += usage.TotalTokens
		if usage.TotalTokens <= 0 || usage.TotalTokens != usage.InputTokens+usage.OutputTokens || tokens > plan.TokenStopLimit {
			a.Error = "judge usage unavailable, inconsistent or over budget"
			save()
			t.Fatal(a.Error)
		}
		if gradeErr != nil {
			a.Error = gradeErr.Error()
		} else if correct, reason, decodeErr := decodePipelineJudgment(raw); decodeErr != nil {
			a.Error = decodeErr.Error()
		} else {
			a.Correct, a.Judged, a.JudgeReason, a.Error = correct, true, reason, ""
		}
		save()
		t.Logf("completed %d/%d calls=%d tokens=%d error=%t", i+1, len(results), calls, tokens, a.Error != "")
		if a.Error != "" {
			t.Errorf("judge failed: %s %s", a.QuestionID, results[i].Arm)
		}
	}
}
