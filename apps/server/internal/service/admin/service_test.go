package admin

import (
	"context"
	"strconv"
	"testing"
	"time"

	conversationstore "memstation/apps/server/internal/store/sql/conversation"
	mediastore "memstation/apps/server/internal/store/sql/media"
	memorystore "memstation/apps/server/internal/store/sql/memory"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/modelcap"
	userservice "memstation/apps/server/internal/service/user"
)

func TestKeyMemoryCountsAreNotLimitedByPreviewPageSize(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	conversations := conversationstore.NewInMemoryConversationRepository()
	memories := memorystore.NewInMemoryMemoryRepository()
	users := userservice.NewService(nil)
	service := NewService(conversations, mediastore.NewInMemoryMediaRepository(), memories, nil, nil, modelcap.NewRegistry(domain.ProviderOpenAICompatible, "", "", "", ""))
	service.AttachUserRuntime(users, nil, nil)

	for idx := range 1005 {
		now := time.Now().UTC().Add(time.Duration(idx) * time.Second)
		if err := memories.UpsertKeyMemory(ctx, domain.KeyMemory{
			ID:        "km-" + strconv.Itoa(idx),
			UserID:    "admin",
			Key:       "key_" + strconv.Itoa(idx),
			Value:     "value",
			CreatedAt: now,
			UpdatedAt: now,
			Metadata:  map[string]any{"node_id": "node-" + strconv.Itoa(idx)},
		}); err != nil {
			t.Fatalf("seed key memory %d: %v", idx, err)
		}
	}

	overview, err := service.Overview(ctx, "admin")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if got := overview["key_memory_count"]; got != 1005 {
		t.Fatalf("expected overview key memory count 1005, got %v", got)
	}

	detail, err := service.UserDetail(ctx, "admin")
	if err != nil {
		t.Fatalf("user detail: %v", err)
	}
	if got := detail.Stats["key_memory_count"]; got != 1005 {
		t.Fatalf("expected detail key memory count 1005, got %v", got)
	}
	if len(detail.KeyMemories) != 20 {
		t.Fatalf("expected detail preview to remain limited to 20, got %d", len(detail.KeyMemories))
	}
}
