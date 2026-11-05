package integration_test

import (
	"testing"

	"memstation/apps/server/internal/bootstrap"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("MEMSTATION_PORT", "")
	t.Setenv("MEMSTATION_MEMORY_PROCESSING_ENDPOINT", "http://127.0.0.1:8092")
	t.Setenv("MEMSTATION_MEMORY_PROCESSING_TOKEN", "test-only-extractor-token")
	t.Setenv("MEMSTATION_MEMORY_PROCESSING_MODEL", "fixture-encoder")
	t.Setenv("MEMSTATION_MEMORY_PROCESSING_REVISION", "fixture-pinned-revision")
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MemoryProcessing.EntityRecallTimeoutMS != 200 {
		t.Fatal("entity recall query budget missing")
	}
	if cfg.Port != 8080 {
		t.Fatalf("expected default port 8080, got %d", cfg.Port)
	}
}
