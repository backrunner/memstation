package bootstrap

import (
	"strings"
	"testing"
)

func TestEmptyAutoKeyWhitelistDisablesLLMConsolidation(t *testing.T) {
	t.Setenv("MEMSTATION_KEY_MEMORY_AUTO_KEYS", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.KeyMemoryAutoKeys) != 0 {
		t.Fatal("empty whitelist unexpectedly enabled automatic writes", cfg.KeyMemoryAutoKeys)
	}
}

func TestLoadConfigRejectsWildcardCredentialedCORSOrigin(t *testing.T) {
	t.Setenv("MEMSTATION_CORS_ALLOWED_ORIGINS", "*")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected wildcard CORS origin to be rejected")
	}
	if !strings.Contains(err.Error(), "cors_allowed_origins must list explicit origins") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigRejectsCrossSiteAdminCookieWithoutSecureFlag(t *testing.T) {
	t.Setenv("MEMSTATION_SESSION_COOKIE_CROSS_SITE", "true")
	t.Setenv("MEMSTATION_SESSION_COOKIE_SECURE", "false")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected insecure cross-site admin cookie config to be rejected")
	}
	if !strings.Contains(err.Error(), "session_cookie_secure must be true") {
		t.Fatalf("unexpected error: %v", err)
	}
}
