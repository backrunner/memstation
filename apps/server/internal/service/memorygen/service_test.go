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

func TestCaptureSourceSnapshotPreservesPartsAndArtifacts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service := NewService(nil, "text-embedding-3-large")
	now := time.Now().UTC()
	conversation := domain.ConversationRecord{
		UserID:         "user-1",
		ConversationID: "conv-artifact",
		RequestFormat:  "chat_completions",
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		FinalizeStatus: domain.FinalizeStatusEnded,
		UpdatedAt:      now,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "please inspect this report"},
		},
		Parts: []domain.MessagePart{{
			PartID:         "part-1",
			UserID:         "user-1",
			ConversationID: "conv-artifact",
			SequenceNum:    0,
			Role:           domain.RoleUser,
			PartType:       domain.ContentBlockFile,
			Filename:       "report.pdf",
			TextContent:    "The report says the launch checklist is pending.",
			CreatedAt:      now,
			UpdatedAt:      now,
		}},
		Artifacts: []domain.MessagePartArtifact{{
			ArtifactID:     "artifact-1",
			UserID:         "user-1",
			ConversationID: "conv-artifact",
			PartID:         "part-1",
			ArtifactType:   "document",
			Status:         "completed",
			TextContent:    "The launch checklist is pending.",
			Confidence:     0.91,
			CreatedAt:      now,
			UpdatedAt:      now,
		}},
	}

	first, created, err := service.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}
	if !created {
		t.Fatal("expected first snapshot to be created")
	}
	if len(first.Parts) != 1 || len(first.Artifacts) != 1 {
		t.Fatalf("expected retained parts/artifacts, got parts=%d artifacts=%d", len(first.Parts), len(first.Artifacts))
	}

	loaded, ok, err := service.GetSourceSnapshot(ctx, "user-1", first.SnapshotID)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if !ok {
		t.Fatal("expected stored snapshot to load")
	}
	if len(loaded.Parts) != 1 || loaded.Parts[0].PartID != "part-1" {
		t.Fatalf("expected source part to round trip, got %+v", loaded.Parts)
	}
	if len(loaded.Artifacts) != 1 || loaded.Artifacts[0].ArtifactID != "artifact-1" {
		t.Fatalf("expected artifact to round trip, got %+v", loaded.Artifacts)
	}

	conversation.Artifacts[0].Status = "failed"
	second, created, err := service.CaptureSourceSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("capture changed snapshot: %v", err)
	}
	if !created || second.SnapshotVersion != 2 {
		t.Fatalf("expected artifact-only change to create version 2, created=%v snapshot=%+v", created, second)
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

func TestCompletedGenerationDoesNotBlockOrAbsorbLaterRebuilds(t *testing.T) {
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

	_, ok, err := service.GetInProgressGeneration(ctx, "user-1")
	if err != nil {
		t.Fatalf("get in-progress generation: %v", err)
	}
	if ok {
		t.Fatal("expected completed candidate generation not to block later rebuilds")
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
	} else if added {
		t.Fatal("expected completed candidate generation not to absorb later snapshots")
	}

	next, nextSources, err := service.CreateRebuildGeneration(ctx, CreateRebuildInput{
		UserID:            "user-1",
		Scope:             "conversation",
		ConversationID:    second.ConversationID,
		ActivateOnSuccess: false,
	})
	if err != nil {
		t.Fatalf("create later rebuild: %v", err)
	}
	if next.GenerationID == generation.GenerationID || len(nextSources) != 1 || nextSources[0].SnapshotID != second.SnapshotID {
		t.Fatalf("unexpected later rebuild: generation=%+v sources=%+v", next, nextSources)
	}

	discarded, err := service.DiscardGeneration(ctx, "user-1", generation.GenerationID)
	if err != nil {
		t.Fatalf("discard completed generation: %v", err)
	}
	if discarded.Status != domain.MemoryGenerationStatusDiscarded {
		t.Fatalf("expected discarded status, got %s", discarded.Status)
	}
	if current, err := service.IsCurrentGenerationSource(ctx, "user-1", generation.GenerationID, first.SnapshotID); err != nil || current {
		t.Fatalf("expected discarded generation source to stop being current, current=%v err=%v", current, err)
	}
}
