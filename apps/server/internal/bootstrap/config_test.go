package bootstrap

import (
	"strings"
	"testing"
	"time"
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

func TestRemovedRerankSettingsRejected(t *testing.T) {
	for _, key := range []string{"MEMSTATION_RERANK_SCORE_POLICY", "MEMSTATION_RERANK_BLEND_WEIGHT"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "")
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("removed setting silently ignored: %v", err)
			}
		})
	}
}

func TestMemoryLLMBudgetConfiguration(t *testing.T) {
	t.Setenv("MEMSTATION_MEMORY_MIN_OUTPUT_TOKENS", "32768")
	t.Setenv("MEMSTATION_MEMORY_MAX_OUTPUT_TOKENS", "131072")
	t.Setenv("MEMSTATION_MEMORY_LLM_TIMEOUT_SECONDS", "600")
	cfg, err := LoadConfig()
	if err != nil || cfg.MemoryMinOutputTokens != 32768 || cfg.MemoryMaxOutputTokens != 131072 || cfg.MemoryLLMTimeout != 10*time.Minute {
		t.Fatal(cfg.MemoryMinOutputTokens, cfg.MemoryMaxOutputTokens, cfg.MemoryLLMTimeout, err)
	}
	t.Setenv("MEMSTATION_MEMORY_MAX_OUTPUT_TOKENS", "16384")
	if _, err = LoadConfig(); err == nil {
		t.Fatal("inverted memory output limits accepted")
	}
}
