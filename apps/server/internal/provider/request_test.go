package provider

import (
	"strings"
	"testing"
)

func TestProviderHTTPErrorRedactsBodyInErrorString(t *testing.T) {
	t.Parallel()

	err := (&ProviderHTTPError{
		StatusCode: 401,
		Body:       `{"error":"bad key sk-secret-token-1234567890 and bearer abcdefghijklmnop", "prompt":"` + strings.Repeat("x", 400) + `"}`,
	}).Error()

	if strings.Contains(err, "sk-secret-token-1234567890") || strings.Contains(err, "abcdefghijklmnop") {
		t.Fatalf("expected provider error to redact credentials, got %q", err)
	}
	if len(err) > 320 {
		t.Fatalf("expected provider error body to be truncated, got length %d: %q", len(err), err)
	}
	if !strings.Contains(err, "status=401") {
		t.Fatalf("expected status code in provider error, got %q", err)
	}
}
