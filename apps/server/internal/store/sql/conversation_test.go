package sql

import (
	"context"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/domain"
)

func TestInMemoryConversationRepositoryAppendsOnlyNewMessages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryConversationRepository()
	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)

	if err := repo.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-append",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "hello"},
			{Role: domain.RoleAssistant, Text: "hi"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist initial exchange: %v", err)
	}

	if err := repo.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-append",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "hello"},
			{Role: domain.RoleAssistant, Text: "hi"},
			{Role: domain.RoleUser, Text: "one more detail"},
			{Role: domain.RoleAssistant, Text: "captured"},
		},
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("persist appended exchange: %v", err)
	}

	conversation, err := repo.GetConversation(ctx, "user-1", "conv-append")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(conversation.Messages) != 4 {
		t.Fatalf("expected 4 stored messages, got %d", len(conversation.Messages))
	}
	if conversation.Messages[2].Text != "one more detail" || conversation.Messages[3].Text != "captured" {
		t.Fatalf("unexpected stored suffix: %+v", conversation.Messages)
	}
	if got := conversation.Metadata["message_storage_mode"]; got != "append" {
		t.Fatalf("expected append storage mode, got %v", got)
	}
}

func TestInMemoryConversationRepositoryProtectsAgainstHistoryRewrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryConversationRepository()
	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)

	initial := domain.ConversationRecord{
		ConversationID: "conv-rewrite",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "hello"},
			{Role: domain.RoleAssistant, Text: "hi"},
			{Role: domain.RoleUser, Text: "the owner is Alice"},
			{Role: domain.RoleAssistant, Text: "stored"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repo.PersistExchange(ctx, initial); err != nil {
		t.Fatalf("persist initial exchange: %v", err)
	}

	if err := repo.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-rewrite",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "hello"},
			{Role: domain.RoleAssistant, Text: "hi"},
			{Role: domain.RoleUser, Text: "the owner is Bob"},
			{Role: domain.RoleAssistant, Text: "updated"},
		},
		CreatedAt: now,
		UpdatedAt: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("persist rewritten exchange: %v", err)
	}

	conversation, err := repo.GetConversation(ctx, "user-1", "conv-rewrite")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(conversation.Messages) != 4 {
		t.Fatalf("expected rewritten conversation to keep 4 messages, got %d", len(conversation.Messages))
	}
	if conversation.Messages[2].Text != "the owner is Bob" || conversation.Messages[3].Text != "updated" {
		t.Fatalf("expected rewritten suffix to replace stale history, got %+v", conversation.Messages)
	}
	if got := conversation.Metadata["message_storage_mode"]; got != "rewrite" {
		t.Fatalf("expected rewrite storage mode, got %v", got)
	}
	if got := conversation.Metadata["history_rewrite_detected"]; got != true {
		t.Fatalf("expected rewrite flag, got %v", got)
	}
}

func TestInMemoryConversationRepositoryKeepsExistingHistoryForPartialSnapshots(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryConversationRepository()
	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)

	if err := repo.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-partial",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "hello"},
			{Role: domain.RoleAssistant, Text: "hi"},
			{Role: domain.RoleUser, Text: "longer history"},
			{Role: domain.RoleAssistant, Text: "captured"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist initial exchange: %v", err)
	}

	if err := repo.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-partial",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		Messages: []domain.UnifiedMessage{
			{Role: domain.RoleUser, Text: "hello"},
			{Role: domain.RoleAssistant, Text: "hi"},
		},
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("persist partial snapshot: %v", err)
	}

	conversation, err := repo.GetConversation(ctx, "user-1", "conv-partial")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(conversation.Messages) != 4 {
		t.Fatalf("expected existing history to be preserved, got %d messages", len(conversation.Messages))
	}
	if got := conversation.Metadata["message_storage_mode"]; got != "partial_snapshot" {
		t.Fatalf("expected partial snapshot mode, got %v", got)
	}
}

func TestInMemoryConversationRepositoryStoresCompactToolMessageDigest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewInMemoryConversationRepository()
	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)
	hugePayload := strings.Repeat("tool schema fragment ", 80)

	if err := repo.PersistExchange(ctx, domain.ConversationRecord{
		ConversationID: "conv-tools",
		UserID:         "user-1",
		RequestFormat:  string(domain.ProtocolOpenAIChat),
		Provider:       domain.ProviderOpenAI,
		Mode:           domain.ModeTransparent,
		Messages: []domain.UnifiedMessage{
			{
				Role: domain.RoleAssistant,
				ToolCalls: []domain.ToolCall{{
					ID:   "call-1",
					Name: "recall_memory",
					Arguments: map[string]any{
						"query": hugePayload + " rollback owner",
						"top_n": 2,
					},
				}},
			},
			{
				Role: domain.RoleTool,
				Text: hugePayload,
				Blocks: []domain.ContentBlock{{
					Type: domain.ContentBlockJSON,
					Raw:  hugePayload,
				}},
				ToolResults: []domain.ToolResult{{
					ToolCallID: "call-1",
					Name:       "recall_memory",
					Output: map[string]any{
						"ok":    true,
						"total": 1,
						"items": []map[string]any{{"key": "owner", "value": "Alice"}},
					},
				}},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist exchange: %v", err)
	}

	conversation, err := repo.GetConversation(ctx, "user-1", "conv-tools")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if digest := conversation.Messages[0].Metadata["memory_prompt_text"]; digest == nil {
		t.Fatalf("expected assistant tool call digest, got %+v", conversation.Messages[0].Metadata)
	}
	if digest := conversation.Messages[1].Metadata["memory_prompt_text"]; digest == nil {
		t.Fatalf("expected tool result digest, got %+v", conversation.Messages[1].Metadata)
	}
	if conversation.Messages[1].Text != "" {
		t.Fatalf("expected stored tool message text to be removed in favor of digest metadata, got %q", conversation.Messages[1].Text)
	}
	if len(conversation.Messages[1].Blocks) != 0 {
		t.Fatalf("expected stored tool message blocks to be removed, got %+v", conversation.Messages[1].Blocks)
	}
}
