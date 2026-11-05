package bootstrap

import (
	"strings"
	"testing"
	"time"
)

func TestEmptyAutoKeyWhitelistDisablesLLMConsolidation(t *testing.T) {
	setTestMemoryEncoderConfig(t)
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
	setTestMemoryEncoderConfig(t)
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
	setTestMemoryEncoderConfig(t)
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
	setTestMemoryEncoderConfig(t)
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

func setTestMemoryEncoderConfig(t *testing.T) {
	t.Helper()
	t.Setenv("MEMSTATION_MEMORY_PROCESSING_ENDPOINT", "http://127.0.0.1:8092")
	t.Setenv("MEMSTATION_MEMORY_PROCESSING_TOKEN", "test-only-extractor-token")
	t.Setenv("MEMSTATION_MEMORY_PROCESSING_MODEL", "fixture-encoder")
	t.Setenv("MEMSTATION_MEMORY_PROCESSING_REVISION", "fixture-pinned-revision")
}

func TestEntityRecallRequiresConfigurationAndRejectsRemovedSwitch(t *testing.T) {
	setTestMemoryEncoderConfig(t)
	cfg, err := LoadConfig()
	if err != nil || cfg.MemoryProcessing.EntityRecallTimeoutMS != 200 {
		t.Fatalf("mandatory entity recall configuration: %+v %v", cfg.MemoryProcessing, err)
	}
	for _, value := range []string{"true", "false", ""} {
		t.Run("removed_switch_"+value, func(t *testing.T) {
			t.Setenv("MEMSTATION_MEMORY_PROCESSING_ENTITY_RECALL_ENABLED", value)
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "unsupported") {
				t.Fatalf("removed switch accepted: %v", err)
			}
		})
	}
	for _, key := range []string{"ENDPOINT", "TOKEN", "MODEL", "REVISION"} {
		t.Setenv("MEMSTATION_MEMORY_PROCESSING_"+key, "")
	}
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "MEMSTATION_MEMORY_PROCESSING_ENDPOINT") {
		t.Fatalf("mandatory extractor settings missing without error: %v", err)
	}
}
