package eval

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/service/processing"
	"memstation/apps/server/internal/testutil/memoryprocessingfixture"
	"memstation/apps/server/internal/worker"
)

func pipelineTestProcessing(t *testing.T) processing.Config {
	t.Helper()
	s, close, err := memoryprocessingfixture.NewWithEntities([]string{"Alex", "Blair"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(close)
	return s.Config()
}

func pipelineReplayProcessing(t *testing.T) processing.Config {
	t.Helper()
	c, err := processing.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPipelineProcessingRequiredBeforeDatabaseOrModelCalls(t *testing.T) {
	for _, mode := range []string{"raw", "derived", "combined"} {
		t.Run(mode, func(t *testing.T) {
			o := LOCOMOPipelineOptions{Modes: []string{mode}, DatabaseURL: "invalid"}
			if _, err := newPipelineRuntime(context.Background(), o); err == nil || !strings.Contains(err.Error(), "pipeline processing") {
				t.Fatal("missing processor reached database", err)
			}
			if err := normalizePipelineProtocol(&o); err == nil || !strings.Contains(err.Error(), "pipeline processing") {
				t.Fatal("missing processor passed protocol", err)
			}
		})
	}
}

func TestPipelineQueueBindsProductionPolicyWithoutMutatingTask(t *testing.T) {
	cfg := memoryprocessingfixture.Config()
	q := &pipelineQueue{processingPolicyID: cfg.Identity(), path: t.TempDir() + "/queue.json", state: pipelineTaskState{Completed: map[string]bool{}}}
	task := domain.TaskMessage{Type: worker.TypeSourceIndex, UniqueKey: "source", Payload: map[string]any{"user_id": "owner"}}
	if err := q.Publish(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, ok := task.Payload["processing_policy_id"]; ok {
		t.Fatal("caller mutated")
	}
	if len(q.state.Pending) != 1 || q.state.Pending[0].Payload["processing_policy_id"] != cfg.Identity() {
		t.Fatal("missing production task policy")
	}
	if err := q.Publish(context.Background(), task); err != nil || len(q.state.Pending) != 1 {
		t.Fatal("duplicate task", err)
	}
	task.Payload["processing_policy_id"] = "different"
	if err := q.Publish(context.Background(), task); err == nil {
		t.Fatal("foreign policy accepted")
	}
}

func TestPipelineSourceModesAndDiagnosticAccounting(t *testing.T) {
	raw := pipelineSearchInput("owner", "question", "raw", 20)
	derived := pipelineSearchInput("owner", "question", "derived", 20)
	combined := pipelineSearchInput("owner", "question", "combined", 20)
	if len(raw.MemoryTypes) != 1 || raw.MemoryTypes[0] != domain.MemoryTypeSegment || !raw.IncludeSegments || raw.IncludeConversation || derived.IncludeSegments || !combined.IncludeSegments {
		t.Fatal("source mode semantics drifted")
	}
	rows := []PipelineAnswer{{Mode: "combined", EntityRecall: &domain.EntityRecallDiagnostics{Status: "complete"}, Judged: true, Correct: true}, {Mode: "combined", EntityRecall: &domain.EntityRecallDiagnostics{Status: "timed_out"}, Error: "reader failure"}}
	s := summarizePipelineAnswers(rows)["combined"]
	if s.Expected != 2 || s.Errors != 1 || s.EntityRecallStatuses["timed_out"] != 1 || s.EntityRecallStatuses["complete"] != 1 {
		t.Fatal("degradation or failure removed", s)
	}
	c := memoryprocessingfixture.Config()
	encoded, err := json.Marshal(c.Public())
	if err != nil || strings.Contains(string(encoded), c.Token) || strings.Contains(string(encoded), c.Endpoint) {
		t.Fatal("processing manifest exposed secret")
	}
}

func TestPipelineProcessingCheckpointDistinguishesDeferredFromCompleted(t *testing.T) {
	for _, test := range []struct {
		strategy, semantic string
		status             domain.ProcessingStatus
		valid              bool
	}{
		{"generative", "queued", domain.ProcessingStatusCompleted, true},
		{"generative", "not_requested", domain.ProcessingStatusSkipped, false},
		{"lightweight", "not_requested", domain.ProcessingStatusSkipped, true},
		{"hybrid", "deferred", domain.ProcessingStatusSkipped, true},
		{"hybrid", "deferred_budget", domain.ProcessingStatusSkipped, true},
		{"hybrid", "queued", domain.ProcessingStatusSkipped, false},
	} {
		policy := memoryprocessingfixture.Config()
		policy.Strategy = test.strategy
		record := domain.ConversationRecord{SummaryStatus: test.status, AnalysisStatus: test.status, Metadata: map[string]any{"source_index_status": "stored", "source_index_generation": "generation", "processing_policy_id": policy.Identity(), "semantic_status": test.semantic}}
		stage := PipelineStage{}
		if err := pipelineStageProcessing(&stage, record, policy); (err == nil) != test.valid {
			t.Fatal(test, err)
		}
		if test.valid && test.status == domain.ProcessingStatusSkipped && stage.SemanticStatus != test.semantic {
			t.Fatal("deferred semantic processing presented as completed")
		}
		record.Metadata["processing_policy_id"] = "foreign"
		if err := pipelineStageProcessing(&stage, record, policy); err == nil {
			t.Fatal("mismatched source policy accepted")
		}
	}
}
