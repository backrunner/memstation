package gateway

import (
	"context"
	"testing"

	"memstation/apps/server/internal/domain"
	settingsservice "memstation/apps/server/internal/service/settings"
)

func TestResolveRequestDefaultsAppliesSourceRetainSetting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	settings := settingsservice.NewService(nil, nil, "gpt-5", "gpt-5-mini", "gpt-5-mini", "text-embedding-3-large", 2048)
	if _, err := settings.UpdateForUser(ctx, "user-1", map[string]any{
		"retain_source_materials": true,
		"tool_write_enabled":      true,
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	service := NewService(nil, nil, nil, settings, domain.ProviderOpenAI)
	resolved, err := service.ResolveRequestDefaults(ctx, domain.UnifiedRequest{
		UserID:        "user-1",
		Protocol:      domain.ProtocolOpenAIChat,
		Mode:          domain.ModeTransparent,
		MemoryPersist: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("resolve defaults: %v", err)
	}
	if resolved.SourceRetain == nil || !*resolved.SourceRetain {
		t.Fatal("expected source retain to follow the user setting")
	}
	if resolved.ToolWriteEnabled {
		t.Fatal("expected tool writes to stay disabled when memory persist is false")
	}
}

func TestResolveRequestDefaultsPreservesExplicitSourceRetainOverride(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	settings := settingsservice.NewService(nil, nil, "gpt-5", "gpt-5-mini", "gpt-5-mini", "text-embedding-3-large", 2048)
	if _, err := settings.UpdateForUser(ctx, "user-1", map[string]any{
		"retain_source_materials": false,
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	service := NewService(nil, nil, nil, settings, domain.ProviderOpenAI)
	resolved, err := service.ResolveRequestDefaults(ctx, domain.UnifiedRequest{
		UserID:       "user-1",
		Protocol:     domain.ProtocolOpenAIChat,
		Mode:         domain.ModeTransparent,
		SourceRetain: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("resolve defaults: %v", err)
	}
	if resolved.SourceRetain == nil || !*resolved.SourceRetain {
		t.Fatal("expected explicit source retain override to win")
	}
}
