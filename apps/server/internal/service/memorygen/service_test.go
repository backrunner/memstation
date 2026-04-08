package memorygen

import (
	"context"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

func TestCaptureSourceSnapshotVersionsConversationHistory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := NewService(nil, "text-embedding-3-large")
	conversation := domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-1",
		RequestFormat:  "chat_completions",
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "hello"},
			{Role: domain.RoleAssistant, Text: "hi there"},
		},
	}

	first, created, err := service.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("capture first snapshot: %v", err)
	}
	if !created {
		t.Fatal("expected first snapshot capture to create a snapshot")
	}
	unchanged, created, err := service.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("capture unchanged snapshot: %v", err)
	}
	if created {
		t.Fatal("expected unchanged conversation to reuse the latest snapshot")
	}
	if unchanged.SnapshotVersion != 1 {
		t.Fatalf("expected unchanged snapshot version 1, got %d", unchanged.SnapshotVersion)
	}

	conversation.Messages = append(conversation.Messages, domain.UnifiedMessage{Role: domain.RoleUser, Text: "new fact"})
	second, created, err := service.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("capture second snapshot: %v", err)
	}
	if !created {
		t.Fatal("expected updated conversation to create a new snapshot")
	}
	if first.SnapshotID == second.SnapshotID {
		t.Fatal("expected updated conversation to produce a new snapshot id")
	}
	if second.SnapshotVersion != 2 {
		t.Fatalf("expected snapshot version 2, got %d", second.SnapshotVersion)
	}
}

func TestCreateRebuildGenerationUsesLatestRetainedSnapshots(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := NewService(nil, "text-embedding-3-large")
	for _, conversationID := range []string{"conv-1", "conv-2"} {
		if _, _, err := service.CaptureSourceSnapshot(ctx, domain.ConversationRecord{
			UserID:         "user-1",
			ConversationID: conversationID,
			RequestFormat:  "chat_completions",
			Provider:       domain.ProviderOpenAI,
			Mode:           domain.ModeTransparent,
			FinalizeStatus: domain.FinalizeStatusEnded,
			UpdatedAt:      time.Now().UTC(),
			Messages: []domain.UnifiedMessage{
				{Role: domain.RoleUser, Text: conversationID},
			},
		}); err != nil {
			t.Fatalf("capture snapshot for %s: %v", conversationID, err)
		}
	}

	generation, sources, err := service.CreateRebuildGeneration(ctx, CreateRebuildInput{
		UserID:            "user-1",
		Scope:             "user",
		ActivateOnSuccess: true,
	})
	if err != nil {
		t.Fatalf("create rebuild generation: %v", err)
	}
	if generation.GenerationID == "" {
		t.Fatal("expected rebuild generation id")
	}
	if generation.SourceCount != 2 {
		t.Fatalf("expected 2 retained snapshots, got %d", generation.SourceCount)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 generation sources, got %d", len(sources))
	}
}

func TestActivateGenerationRequiresCompletedBuild(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := NewService(nil, "text-embedding-3-large")
	if _, _, err := service.CaptureSourceSnapshot(ctx, domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-1",
		RequestFormat:  "chat_completions",
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "keep a retained snapshot"},
		},
	}); err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}

	generation, sources, err := service.CreateRebuildGeneration(ctx, CreateRebuildInput{
		UserID:            "user-1",
		Scope:             "user",
		ActivateOnSuccess: false,
	})
	if err != nil {
		t.Fatalf("create rebuild generation: %v", err)
	}
	if _, err := service.ActivateGeneration(ctx, "user-1", generation.GenerationID); err == nil {
		t.Fatal("expected incomplete generation activation to fail")
	}

	if _, ready, err := service.MarkGenerationSourceCompleted(ctx, "user-1", generation.GenerationID, sources[0].SnapshotID, 0); err != nil {
		t.Fatalf("mark generation source completed: %v", err)
	} else if ready {
		t.Fatal("expected manual-activation generation to remain inactive after completion")
	}
	generation, err = service.GetGeneration(ctx, "user-1", generation.GenerationID)
	if err != nil {
		t.Fatalf("reload completed generation: %v", err)
	}
	if generation.Status != domain.MemoryGenerationStatusCompleted {
		t.Fatalf("expected manual-activation generation to become completed, got %s", generation.Status)
	}

	activated, err := service.ActivateGeneration(ctx, "user-1", generation.GenerationID)
	if err != nil {
		t.Fatalf("activate completed generation: %v", err)
	}
	if !activated.IsActive {
		t.Fatal("expected generation to become active after activation")
	}
}

func TestCreateConversationScopedRebuildRejectsAutoActivation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := NewService(nil, "text-embedding-3-large")
	if _, _, err := service.CaptureSourceSnapshot(ctx, domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-1",
		RequestFormat:  "chat_completions",
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "retained snapshot for a conversation-scoped rebuild"},
		},
	}); err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}

	if _, _, err := service.CreateRebuildGeneration(ctx, CreateRebuildInput{
		UserID:            "user-1",
		Scope:             "conversation",
		ConversationID:    "conv-1",
		ActivateOnSuccess: true,
	}); err == nil {
		t.Fatal("expected conversation-scoped rebuild with auto activation to be rejected")
	}
}

func TestConversationScopedGenerationCannotBeActivated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := NewService(nil, "text-embedding-3-large")
	if _, _, err := service.CaptureSourceSnapshot(ctx, domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-1",
		RequestFormat:  "chat_completions",
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "retained snapshot for a conversation candidate"},
		},
	}); err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}

	generation, sources, err := service.CreateRebuildGeneration(ctx, CreateRebuildInput{
		UserID:            "user-1",
		Scope:             "conversation",
		ConversationID:    "conv-1",
		ActivateOnSuccess: false,
	})
	if err != nil {
		t.Fatalf("create rebuild generation: %v", err)
	}
	if _, ready, err := service.MarkGenerationSourceCompleted(ctx, "user-1", generation.GenerationID, sources[0].SnapshotID, 0); err != nil {
		t.Fatalf("mark generation source completed: %v", err)
	} else if ready {
		t.Fatal("expected conversation-scoped generation to require manual review only")
	}
	if _, err := service.ActivateGeneration(ctx, "user-1", generation.GenerationID); err == nil {
		t.Fatal("expected conversation-scoped generation activation to be rejected")
	}
}

func TestIsCurrentGenerationSourceTracksLatestSnapshotPerConversation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := NewService(nil, "text-embedding-3-large")
	conversation := domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-1",
		RequestFormat:  "chat_completions",
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "initial snapshot"},
		},
	}

	first, _, err := service.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("capture first snapshot: %v", err)
	}
	generation, _, err := service.CreateRebuildGeneration(ctx, CreateRebuildInput{
		UserID:            "user-1",
		Scope:             "conversation",
		ConversationID:    "conv-1",
		ActivateOnSuccess: false,
	})
	if err != nil {
		t.Fatalf("create rebuild generation: %v", err)
	}

	current, err := service.IsCurrentGenerationSource(ctx, "user-1", generation.GenerationID, first.SnapshotID)
	if err != nil {
		t.Fatalf("check first snapshot freshness: %v", err)
	}
	if !current {
		t.Fatal("expected first snapshot to be current before a newer snapshot exists")
	}

	conversation.Messages = append(conversation.Messages, domain.UnifiedMessage{Role: domain.RoleAssistant, Text: "newer retained state"})
	second, _, err := service.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("capture second snapshot: %v", err)
	}
	if _, added, err := service.AddSnapshotToGeneration(ctx, generation.GenerationID, second); err != nil {
		t.Fatalf("add second snapshot to generation: %v", err)
	} else if !added {
		t.Fatal("expected second snapshot to be added to the in-progress generation")
	}

	current, err = service.IsCurrentGenerationSource(ctx, "user-1", generation.GenerationID, first.SnapshotID)
	if err != nil {
		t.Fatalf("check superseded snapshot freshness: %v", err)
	}
	if current {
		t.Fatal("expected older snapshot to stop being current after a newer version is added")
	}

	current, err = service.IsCurrentGenerationSource(ctx, "user-1", generation.GenerationID, second.SnapshotID)
	if err != nil {
		t.Fatalf("check latest snapshot freshness: %v", err)
	}
	if !current {
		t.Fatal("expected newest snapshot to be current for the generation")
	}
}

func TestCompletedGenerationStillAcceptsNewSnapshots(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := NewService(nil, "text-embedding-3-large")
	firstConversation := domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-1",
		RequestFormat:  "chat_completions",
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "first retained conversation"},
		},
	}

	first, _, err := service.CaptureSourceSnapshot(ctx, firstConversation)
	if err != nil {
		t.Fatalf("capture first snapshot: %v", err)
	}
	generation, sources, err := service.CreateRebuildGeneration(ctx, CreateRebuildInput{
		UserID:            "user-1",
		Scope:             "user",
		ActivateOnSuccess: false,
	})
	if err != nil {
		t.Fatalf("create rebuild generation: %v", err)
	}
	if len(sources) != 1 || sources[0].SnapshotID != first.SnapshotID {
		t.Fatalf("expected one queued source for the first snapshot, got %+v", sources)
	}
	if _, ready, err := service.MarkGenerationSourceCompleted(ctx, "user-1", generation.GenerationID, first.SnapshotID, 0); err != nil {
		t.Fatalf("mark first snapshot completed: %v", err)
	} else if ready {
		t.Fatal("expected manual-activation generation to stay pending activation")
	}

	inProgress, ok, err := service.GetInProgressGeneration(ctx, "user-1")
	if err != nil {
		t.Fatalf("get in-progress generation: %v", err)
	}
	if !ok || inProgress.GenerationID != generation.GenerationID {
		t.Fatalf("expected completed candidate generation to remain discoverable, got %+v ok=%v", inProgress, ok)
	}
	if inProgress.Status != domain.MemoryGenerationStatusCompleted {
		t.Fatalf("expected completed candidate status, got %s", inProgress.Status)
	}

	second, _, err := service.CaptureSourceSnapshot(ctx, domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-2",
		RequestFormat:  "chat_completions",
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      time.Now().UTC(),
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "new retained conversation after candidate completion"},
		},
	})
	if err != nil {
		t.Fatalf("capture second snapshot: %v", err)
	}
	if _, added, err := service.AddSnapshotToGeneration(ctx, generation.GenerationID, second); err != nil {
		t.Fatalf("add second snapshot to completed generation: %v", err)
	} else if !added {
		t.Fatal("expected completed candidate generation to accept new snapshots")
	}

	updated, err := service.GetGeneration(ctx, "user-1", generation.GenerationID)
	if err != nil {
		t.Fatalf("reload updated generation: %v", err)
	}
	if updated.SourceCount != 2 {
		t.Fatalf("expected source count to grow to 2, got %d", updated.SourceCount)
	}
	if updated.Status != domain.MemoryGenerationStatusRunning {
		t.Fatalf("expected completed generation to return to running after new work arrives, got %s", updated.Status)
	}
}
