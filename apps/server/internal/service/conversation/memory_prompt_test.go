package conversation

import (
	"strings"
	"testing"

	"memstation/apps/server/internal/domain"
)

func TestBuildMemoryPromptInputCompactsToolIOWithoutDroppingMeaning(t *testing.T) {
	t.Parallel()

	hugePayload := strings.Repeat("tool schema fragment ", 80)
	messages := PrepareMessagesForStorage([]domain.UnifiedMessage{
		{
			Role: domain.RoleUser,
			Text: "Please recall the rollout owner and the latest deployment notes.",
		},
		{
			Role: domain.RoleAssistant,
			Text: "I'll look that up in memory.",
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
			ToolResults: []domain.ToolResult{{
				ToolCallID: "call-1",
				Name:       "recall_memory",
				Output: map[string]any{
					"ok":    true,
					"total": 2,
					"items": []map[string]any{
						{
							"type":    "summary",
							"content": "Rollback owner is Alice and migrations should run in small batches.",
						},
						{
							"key":   "deployment_note",
							"value": "Keep the deployment playbook visible during rollout.",
						},
					},
					"debug": hugePayload,
				},
			}},
		},
		{
			Role: domain.RoleAssistant,
			Text: "Rollback owner is Alice, and we should keep the deployment playbook visible.",
		},
	})
	summary := BuildMemoryPromptInput(messages)

	if !strings.Contains(summary, "recall_memory(") {
		t.Fatalf("expected compact summary to retain tool name, got %q", summary)
	}
	if !strings.Contains(summary, "top_n=2") {
		t.Fatalf("expected compact summary to retain tool arguments, got %q", summary)
	}
	if !strings.Contains(summary, "total=2") {
		t.Fatalf("expected compact summary to retain tool result counts, got %q", summary)
	}
	if !strings.Contains(summary, "Alice") {
		t.Fatalf("expected compact summary to retain result semantics, got %q", summary)
	}
	if len(summary) > defaultMemoryPromptMaxLength+3 {
		t.Fatalf("expected compact summary to stay bounded, got len=%d summary=%q", len(summary), summary)
	}
	if strings.Count(summary, "tool schema fragment") > 3 {
		t.Fatalf("expected huge tool payload to be compacted, got %q", summary)
	}
	if digest := messages[1].Metadata[MemoryPromptTextMetadataKey]; digest == nil {
		t.Fatalf("expected tool call message to cache memory prompt text, got %+v", messages[1].Metadata)
	}
	if digest := messages[2].Metadata[MemoryPromptTextMetadataKey]; digest == nil {
		t.Fatalf("expected tool result message to cache memory prompt text, got %+v", messages[2].Metadata)
	}
	if messages[2].Text != "" {
		t.Fatalf("expected stored tool text to be removed in favor of digest metadata, got %q", messages[2].Text)
	}
	if len(messages[2].Blocks) != 0 {
		t.Fatalf("expected stored tool blocks to be removed, got %+v", messages[2].Blocks)
	}
}

func TestBuildMemoryPromptInputCompactsJSONBlocks(t *testing.T) {
	t.Parallel()

	summary := BuildMemoryPromptInput([]domain.UnifiedMessage{{
		Role: domain.RoleAssistant,
		Blocks: []domain.ContentBlock{{
			Type: domain.ContentBlockJSON,
			Raw:  `{"event":"weather_lookup","location":"Shanghai","forecast":"rain","temperature_c":[18,22],"notes":"pack an umbrella"}`,
		}},
	}})

	if !strings.Contains(summary, "weather_lookup") {
		t.Fatalf("expected JSON block to be compacted into readable summary, got %q", summary)
	}
	if !strings.Contains(summary, "Shanghai") {
		t.Fatalf("expected JSON block to retain salient fields, got %q", summary)
	}
}
