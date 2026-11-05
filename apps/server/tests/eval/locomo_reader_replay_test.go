package eval

import (
	"reflect"
	"testing"

	"memstation/apps/server/internal/domain"
	memoryservice "memstation/apps/server/internal/service/memory"
)

func readerComparisonFixture() (packetReplayPlan, preparedPacketReplay) {
	docs := []domain.MemoryDocument{{ID: "record", NodeID: "node", UserID: "owner", Content: "An independent source."}}
	ids := map[string]string{"memory-1": "record"}
	plan := packetReplayPlan{ReaderPolicy: "interpretation", Cases: []packetReplayCase{{Documents: docs, Baseline: PipelineAnswer{Context: "frozen evidence", ContextSourceIDs: ids}}}}
	prompt := pipelineReaderInstruction("interpretation")
	prepared := preparedPacketReplay{Preparation: map[string]string{"kind": "frozen_reader_comparison"}, Packets: []memoryservice.EvidencePacket{{Text: "frozen evidence", Memories: append([]domain.MemoryDocument(nil), docs...), SourceIDs: map[string]string{"memory-1": "record"}}}, ReaderComparison: &frozenReaderComparison{BaselineInstruction: "Prior frozen instruction.", BaselineSHA256: pipelineHash("Prior frozen instruction."), CandidateInstruction: prompt, CandidateSHA256: pipelineHash(prompt)}}
	return plan, prepared
}

func TestReaderReplayRejectsMixedChangesAndUnfrozenInstructions(t *testing.T) {
	p, prepared := readerComparisonFixture()
	prompts, err := packetReplayInstructions(p, prepared)
	if err != nil || prompts["baseline"] != prepared.ReaderComparison.BaselineInstruction || prompts["candidate"] != pipelineReaderInstruction(p.ReaderPolicy) {
		t.Fatal("valid reader comparison failed", err)
	}
	for name, mutate := range map[string]func(*preparedPacketReplay){
		"missing provenance":     func(p *preparedPacketReplay) { p.Preparation["kind"] = "same_selected_records" },
		"missing instructions":   func(p *preparedPacketReplay) { p.ReaderComparison = nil },
		"changed baseline":       func(p *preparedPacketReplay) { p.ReaderComparison.BaselineInstruction += " changed" },
		"changed candidate hash": func(p *preparedPacketReplay) { p.ReaderComparison.CandidateSHA256 = "changed" },
		"candidate differs from code": func(p *preparedPacketReplay) {
			p.ReaderComparison.CandidateInstruction = "other"
			p.ReaderComparison.CandidateSHA256 = pipelineHash("other")
		},
		"same prompt": func(p *preparedPacketReplay) {
			p.ReaderComparison.BaselineInstruction = p.ReaderComparison.CandidateInstruction
			p.ReaderComparison.BaselineSHA256 = p.ReaderComparison.CandidateSHA256
		},
		"changed context":           func(p *preparedPacketReplay) { p.Packets[0].Text += " added" },
		"changed source map":        func(p *preparedPacketReplay) { p.Packets[0].SourceIDs["memory-1"] = "other" },
		"changed canonical content": func(p *preparedPacketReplay) { p.Packets[0].Memories[0].Content = "other" },
		"missing packet":            func(p *preparedPacketReplay) { p.Packets = nil },
	} {
		t.Run(name, func(t *testing.T) {
			p, prepared := readerComparisonFixture()
			mutate(&prepared)
			if _, err := packetReplayInstructions(p, prepared); err == nil {
				t.Fatal("invalid comparison accepted before inference")
			}
		})
	}
}

func TestReaderPayloadExcludesEvaluationLabelsAndEmptyMemory(t *testing.T) {
	answer := PipelineAnswer{Query: "What was agreed?", Context: "Authorized evidence", Expected: "private reference", Category: 3, QuestionID: "private-case", SampleID: "private-sample", Correct: true, JudgeReason: "private grade", EvidenceRefs: []string{"private-label"}}
	if got := packetReaderPayload(answer, false); !reflect.DeepEqual(got, map[string]any{"question": answer.Query, "memories": answer.Context}) {
		t.Fatal("reader payload leaked offline labels")
	}
	if got := packetReaderPayload(answer, true); !reflect.DeepEqual(got, map[string]any{"question": answer.Query, "memories": ""}) {
		t.Fatal("empty-memory diagnostic retained source or labels")
	}
}

func TestContextReplaysKeepIdenticalCurrentReaderInstructions(t *testing.T) {
	for _, kind := range []string{"same_selected_records", "frozen_query_provider_retrieval", "frozen_source_support"} {
		t.Run(kind, func(t *testing.T) {
			plan := packetReplayPlan{ReaderPolicy: "interpretation"}
			prepared := preparedPacketReplay{Preparation: map[string]string{"kind": kind}}
			got, err := packetReplayInstructions(plan, prepared)
			want := pipelineReaderInstruction(plan.ReaderPolicy)
			if err != nil || len(got) != 2 || got["baseline"] != want || got["candidate"] != want {
				t.Fatal("context comparison silently changed reader instructions", err)
			}
		})
	}
}

func TestPipelineFinalAnswerGradesOnlyTheFinalLine(t *testing.T) {
	got, err := pipelineFinalAnswer("reasoning", "1. FINAL ANSWER: draft\n2. checked\nFINAL ANSWER:  in May 2023 ")
	if err != nil || got != "in May 2023" {
		t.Fatal(got, err)
	}
	for _, reply := range []string{"worked it out, no marker", "FINAL ANSWER:   "} {
		if _, err := pipelineFinalAnswer("reasoning", reply); err == nil {
			t.Fatal("reply without a usable final answer graded", reply)
		}
	}
	if got, err := pipelineFinalAnswer("interpretation", "plain reply"); err != nil || got != "plain reply" {
		t.Fatal("other readers changed", got, err)
	}
}

func TestReaderEffortComparisonAllowsTheSamePromptOnlyWhenDeclared(t *testing.T) {
	p, prepared := readerComparisonFixture()
	prepared.ReaderComparison.BaselineInstruction = prepared.ReaderComparison.CandidateInstruction
	prepared.ReaderComparison.BaselineSHA256 = prepared.ReaderComparison.CandidateSHA256
	if _, err := packetReplayInstructions(p, prepared); err == nil {
		t.Fatal("same prompt accepted without a declared effort change")
	}
	p.CandidateReasoningEffort = "medium"
	if _, err := packetReplayInstructions(p, prepared); err != nil {
		t.Fatal(err)
	}
}
