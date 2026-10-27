package worker

import (
	"context"
	"errors"
	"testing"

	"memstation/apps/server/internal/domain"
	memorygenservice "memstation/apps/server/internal/service/memorygen"
)

func TestRebuildCompletionRetryAndLateActivation(t *testing.T) {
	ctx := context.Background()
	generations := memorygenservice.NewService(nil, "fixture")
	if _, _, err := generations.CaptureSourceSnapshot(ctx, domain.ConversationRecord{UserID: "owner", ConversationID: "retained", Messages: []domain.UnifiedMessage{{Role: domain.RoleUser, Text: "retained source"}}, AgentID: "fixture-agent"}); err != nil {
		t.Fatal(err)
	}
	g, sources, err := generations.CreateRebuildGeneration(ctx, memorygenservice.CreateRebuildInput{UserID: "owner", ActivateOnSuccess: true})
	if err != nil {
		t.Fatal(err)
	}
	publisher := &failFirstPublisher{}
	services := Services{Generations: generations, Publisher: publisher, TaskAuthority: fixtureTaskAuthority{}}
	payload := MemoryRebuildConversationPayload{UserID: "owner", GenerationID: g.GenerationID, SnapshotID: sources[0].SnapshotID}
	if err := completeRebuildSource(ctx, services, payload, 3); !errors.Is(err, ErrQueueUnavailable) {
		t.Fatal("expected activation publication failure", err)
	}
	// A completed-source retry must only republish, without extraction dependencies.
	handler := handleMemoryRebuildConversation(nilLogger(), services)
	if err := handler(ctx, newTask(t, TypeMemoryRebuildConversation, map[string]any{"user_id": "owner", "generation_id": g.GenerationID, "snapshot_id": sources[0].SnapshotID})); err != nil {
		t.Fatal(err)
	}
	if len(publisher.tasks) != 1 || publisher.tasks[0].Type != TypeMemoryRebuildActivate {
		t.Fatal("missing activation retry", publisher.tasks)
	}
	state, err := generations.GetGeneration(ctx, "owner", g.GenerationID)
	if err != nil || state.ProcessedCount != 1 || state.EmbeddingCount != 3 {
		t.Fatal("retry changed completion counts", state, err)
	}
	activate := handleMemoryRebuildActivate(nilLogger(), services)
	task := newTask(t, TypeMemoryRebuildActivate, map[string]any{"user_id": "owner", "generation_id": g.GenerationID})
	if err := activate(ctx, task); err != nil {
		t.Fatal(err)
	}
	next, nextSources, err := generations.CreateRebuildGeneration(ctx, memorygenservice.CreateRebuildInput{UserID: "owner", ActivateOnSuccess: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = generations.MarkGenerationSourceCompleted(ctx, "owner", next.GenerationID, nextSources[0].SnapshotID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = generations.ActivateCompletedGeneration(ctx, "owner", next.GenerationID); err != nil {
		t.Fatal(err)
	}
	if err = activate(ctx, task); err != nil {
		t.Fatal(err)
	}
	current, err := generations.EnsureActiveGeneration(ctx, "owner")
	if err != nil || current.GenerationID != next.GenerationID {
		t.Fatal("late activation replaced newer generation", current, err)
	}
}
