package unit_test

import (
	"net/http/httptest"
	"testing"

	"memstation/apps/server/internal/security"
)

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	t.Parallel()
	limiter := security.NewRateLimiter(100, 2)
	if limiter == nil {
		t.Fatal("expected limiter")
	}
	if !limiter.Allow("user-1") {
		t.Fatal("expected first request to pass")
	}
	if !limiter.Allow("user-1") {
		t.Fatal("expected second request to pass")
	}
	if limiter.Allow("user-1") {
		t.Fatal("expected third request to be rate limited")
	}
}

func TestRateLimitKeyIncludesUserAndAPIKeyFingerprint(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest("GET", "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer ms-local-dev")
	key := security.RateLimitKey("user-1", request)
	if key == "" || key == "user-1" {
		t.Fatalf("expected composite key, got %q", key)
	}
}
