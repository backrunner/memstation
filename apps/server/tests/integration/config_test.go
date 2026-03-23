package integration_test

import (
	"os"
	"testing"

	"memstation/apps/server/internal/bootstrap"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Parallel()
	_ = os.Unsetenv("MEMSTATION_PORT")
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("expected default port 8080, got %d", cfg.Port)
	}
}
