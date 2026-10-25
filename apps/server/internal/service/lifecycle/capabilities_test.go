package lifecycle

import (
	"context"
	"errors"
	"testing"

	"memstation/apps/server/internal/domain"
	"memstation/apps/server/internal/security"
)

func TestProbeUsesEffectivePermissionsAndRecoveryAvailability(t *testing.T) {
	s := NewService(nil)
	ctx := security.WithAccess(context.Background(), domain.AccessContext{WorkspaceID: "group", Capabilities: []string{domain.CapabilityMemoryRead}})
	for _, check := range []func(context.Context) error{nil, func(context.Context) error { return errors.New("unavailable") }} {
		s.SetReady(check)
		result := s.Probe(ctx, "test")
		if result.Status != "memory-disabled" {
			t.Fatal("unavailable memory advertised", result)
		}
		for key, value := range result.Capabilities {
			if value {
				t.Fatal("unavailable capability", key)
			}
		}
	}
	s.SetReady(func(context.Context) error { return nil })
	result := s.Probe(ctx, "test")
	if result.Status != "ready" || !result.Capabilities["multi_workspace"] || !result.Capabilities["memory_inspect"] || result.Capabilities["independent_ingestion"] || result.Capabilities["publishing"] {
		t.Fatal("ineffective capabilities advertised", result)
	}
}
