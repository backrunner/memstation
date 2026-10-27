package eval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"

	"github.com/hibiken/asynq"
)

func pipelineFixture(t *testing.T) string {
	t.Helper()
	sample := map[string]any{"sample_id": "synthetic", "conversation": map[string]any{
		"session_2_date_time": "2:00 pm on 3 March, 2025", "session_2": []any{map[string]any{"dia_id": "D2:1", "speaker": "Blair", "text": "I changed my plan."}},
		"session_1_date_time": "1:00 pm on 2 March, 2025", "session_1": []any{map[string]any{"dia_id": "D1:1", "speaker": "Alex", "text": "I own a red bicycle."}, map[string]any{"dia_id": "D1:2", "speaker": "Blair", "text": "I prefer walking."}},
		"event_summary": "GOLD_SENTINEL_DO_NOT_INGEST",
	}, "qa": []any{
		map[string]any{"question": "A question without evidence?", "answer": "GOLD_SENTINEL_DO_NOT_INGEST", "category": 3},
		map[string]any{"question": "A supported question?", "answer": "red", "category": 4, "evidence": []string{"D1:1"}},
		map[string]any{"question": "Adversarial?", "answer": "unknown", "category": 5},
	}, "observation": "GOLD_SENTINEL_DO_NOT_INGEST"}
	raw, _ := json.Marshal([]any{sample})
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPipelineSourcesPreserveHumanAttributionAndExcludeGold(t *testing.T) {
	t.Parallel()
	samples, err := loadPipelineSamples(pipelineFixture(t), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || len(samples[0].Questions) != 2 || len(samples[0].Questions[0].Evidence) != 0 {
		t.Fatal("QA denominator dropped no-evidence questions")
	}
	if samples[0].Sessions[0].ID != "session_1" {
		t.Fatal("sessions not chronological")
	}
	raw, _ := json.Marshal(samples[0].Sessions)
	if strings.Contains(string(raw), "GOLD_SENTINEL") {
		t.Fatal("gold leaked into sources")
	}
	for _, s := range samples[0].Sessions {
		for _, m := range s.Messages {
			if m.Role != domain.RoleUser || !strings.Contains(m.Text, "Speaker:") || !strings.Contains(m.Text, "Conversation date:") {
				t.Fatal("lost source attribution or date")
			}
		}
	}
	a, err := loadPipelineSamples(pipelineFixture(t), "synthetic", 1)
	if err != nil || len(a[0].Sessions) != 2 || len(a[0].Questions) != 2 {
		t.Fatal("sampling changed corpus")
	}
	for _, id := range []string{"../data", "a/b", "", "a.b"} {
		if safePipelineID(id) {
			t.Errorf("unsafe identity accepted: %q", id)
		}
	}
}

func TestPipelineContextBudgetAndCitationMapping(t *testing.T) {
	t.Parallel()
	docs := []domain.MemoryDocument{
		{Type: domain.MemoryTypeFact, Content: strings.Repeat("x", 400)},
		{Type: domain.MemoryTypeFact, Content: "A is not B", ConversationID: "owner-session_1", Metadata: map[string]any{"source_quotes": []any{map[string]any{"sequence_num": 2, "sequence_base": 1}}}},
		{Type: domain.MemoryTypeFact, Content: "A is not B"},
		{Type: domain.MemoryTypeSegment, Content: "source", ConversationID: "owner-session_1", Metadata: map[string]any{"start_sequence": 0, "end_sequence": 0}},
		{Type: domain.MemoryTypeSummary, Content: "summary", ConversationID: "owner-session_1"},
	}
	text, packed := packPipelineContext(docs, 100)
	if len(text) > 100 || len(packed) != 3 || !strings.Contains(text, "not B") {
		t.Fatal("packing truncated or duplicated evidence")
	}
	refs := pipelineSourceRefs(packed, "owner", pipelineSample{Sessions: []pipelineSession{{ID: "session_1", Refs: []string{"D1:1", "D1:2", "D1:3"}}}})
	if strings.Join(refs, ",") != "D1:1,D1:2" {
		t.Fatalf("summary must not claim whole session: %v", refs)
	}
}

func TestPipelineSummaryRetainsFailuresInDenominator(t *testing.T) {
	t.Parallel()
	s := summarizePipelineAnswers([]PipelineAnswer{{Mode: "raw", Correct: true, Judged: true}, {Mode: "raw", Error: "not executed"}, {Mode: "raw", Error: "judge failed"}, {Mode: "raw", Judged: true}})["raw"]
	if s.Expected != 4 || s.Correct != 1 || s.Accuracy != .25 || s.Coverage != .5 || s.Errors != 2 {
		t.Fatalf("inflated summary: %+v", s)
	}
}

func TestPipelineCitationMappingUsesExplicitZeroBasedQuotePositions(t *testing.T) {
	for _, q := range []map[string]any{{"sequence_num": 0, "sequence_base": 0}, {"sequence_num": 0, "sequence_base": 0, "part_id": "part"}, {"sequence_num": 1, "sequence_base": 1}} {
		docs := []domain.MemoryDocument{{ConversationID: "owner-session_1", Metadata: map[string]any{"source_quotes": []any{q}}}}
		got := pipelineSourceRefs(docs, "owner", pipelineSample{Sessions: []pipelineSession{{ID: "session_1", Refs: []string{"D1:1", "D1:2"}}}})
		if strings.Join(got, ",") != "D1:1" {
			t.Fatal("wrong source turn reported", q, got)
		}
	}
}

func TestPipelineQueuePersistsFanoutAndRefusesForeignScope(t *testing.T) {
	t.Parallel()
	ctx, err := security.WithPersonalDataScope(context.Background(), "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	q := &pipelineQueue{path: filepath.Join(t.TempDir(), "tasks.json"), state: pipelineTaskState{Completed: map[string]bool{}}}
	first := domain.TaskMessage{Type: "first", Payload: map[string]any{"user_id": "owner"}}
	if err = q.Publish(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err = q.Publish(ctx, domain.TaskMessage{Type: "bad", Payload: map[string]any{"user_id": "foreign"}}); err == nil {
		t.Fatal("foreign task accepted")
	}
	mux := asynq.NewServeMux()
	calls := 0
	mux.HandleFunc("first", func(ctx context.Context, _ *asynq.Task) error {
		return q.Publish(ctx, domain.TaskMessage{Type: "second", Payload: map[string]any{"user_id": "owner"}})
	})
	mux.HandleFunc("second", func(context.Context, *asynq.Task) error {
		calls++
		return errors.Join(errors.New("injected interruption"), asynq.SkipRetry)
	})
	if err = q.drain(ctx, mux); err == nil {
		t.Fatal("failure swallowed")
	}
	raw, err := os.ReadFile(q.path)
	if err != nil {
		t.Fatal(err)
	}
	resumed := &pipelineQueue{path: q.path}
	if err = json.Unmarshal(raw, &resumed.state); err != nil {
		t.Fatal(err)
	}
	if len(resumed.state.Pending) != 1 || resumed.state.Pending[0].Type != "second" || calls != 1 {
		t.Fatal("lost pending task or retried nonretryable failure")
	}
	if len(resumed.state.Attempts) != 2 || resumed.state.Attempts[0].Error != "" || resumed.state.Attempts[1].Error == "" {
		t.Fatal("successful or failed task attempt was lost")
	}
	mux = asynq.NewServeMux()
	mux.HandleFunc("second", func(context.Context, *asynq.Task) error { calls++; return nil })
	if err = resumed.drain(ctx, mux); err != nil {
		t.Fatal(err)
	}
	if err = resumed.Publish(ctx, first); err != nil {
		t.Fatal(err)
	}
	if len(resumed.state.Pending) != 0 || calls != 2 {
		t.Fatal("completed task duplicated")
	}
	if len(resumed.state.Attempts) != 3 || resumed.state.Attempts[1].Error == "" || resumed.state.Attempts[2].Error != "" {
		t.Fatal("recovery overwrote original failure")
	}
}

func TestPipelineQueueConcurrentFanoutPreservesPerTaskScope(t *testing.T) {
	q := &pipelineQueue{path: filepath.Join(t.TempDir(), "tasks.json"), concurrency: 2, state: pipelineTaskState{Completed: map[string]bool{}}}
	for _, user := range []string{"first-owner", "second-owner"} {
		ctx, err := security.WithPersonalDataScope(context.Background(), user, "")
		if err != nil {
			t.Fatal(err)
		}
		if err = q.Publish(ctx, domain.TaskMessage{Type: "root", Payload: map[string]any{"user_id": user}}); err != nil {
			t.Fatal(err)
		}
	}
	mux := asynq.NewServeMux()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	counts := map[string]int{}
	handler := func(ctx context.Context, task *asynq.Task) error {
		var payload struct {
			User      string `json:"user_id"`
			Workspace string `json:"workspace_id"`
		}
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		bound, err := security.WithDataScope(ctx, payload.User, payload.Workspace, "")
		if err != nil {
			return err
		}
		mu.Lock()
		counts[payload.User]++
		mu.Unlock()
		if task.Type() == "root" {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return q.Publish(bound, domain.TaskMessage{Type: "child", Payload: map[string]any{"user_id": payload.User}})
		}
		return nil
	}
	mux.HandleFunc("root", handler)
	mux.HandleFunc("child", handler)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.drain(ctx, mux) }()
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			close(release)
			t.Fatal("worker fanout serialized")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(q.state.Pending) != 0 || len(q.state.Attempts) != 4 || counts["first-owner"] != 2 || counts["second-owner"] != 2 {
		t.Fatal("cross-owner fanout lost or rebound", q.state, counts)
	}
}

func TestPipelineTaskFailureDoesNotBlockAnotherOwnerOrFanout(t *testing.T) {
	q := &pipelineQueue{path: filepath.Join(t.TempDir(), "tasks.json"), concurrency: 1, state: pipelineTaskState{Completed: map[string]bool{}}}
	for _, user := range []string{"broken-owner", "healthy-owner"} {
		scoped, err := security.WithPersonalDataScope(context.Background(), user, "")
		if err != nil {
			t.Fatal(err)
		}
		if err = q.Publish(scoped, domain.TaskMessage{Type: "root", Payload: map[string]any{"user_id": user}}); err != nil {
			t.Fatal(err)
		}
	}
	mux := asynq.NewServeMux()
	healthyCalls := 0
	brokenCalls := 0
	mux.HandleFunc("root", func(ctx context.Context, task *asynq.Task) error {
		var payload map[string]any
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return err
		}
		owner := payload["user_id"].(string)
		if owner == "broken-owner" {
			brokenCalls++
			return errors.Join(errors.New("invalid extraction"), asynq.SkipRetry)
		}
		healthyCalls++
		scoped, err := security.WithPersonalDataScope(ctx, owner, "")
		if err != nil {
			return err
		}
		return q.Publish(scoped, domain.TaskMessage{Type: "child", Payload: map[string]any{"user_id": owner}})
	})
	mux.HandleFunc("child", func(context.Context, *asynq.Task) error { healthyCalls++; return nil })
	err := q.drain(context.Background(), mux)
	var failures pipelineTaskFailures
	if !errors.As(err, &failures) || failures.forOwner("healthy-owner") != nil || failures.forOwner("broken-owner") == nil || healthyCalls != 2 || brokenCalls != 1 || len(q.state.Pending) != 1 {
		t.Fatal("task failure crossed owners or starved healthy fanout", err, healthyCalls, brokenCalls, q.state)
	}
}
