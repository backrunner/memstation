package unit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"memstation/apps/server/internal/security"
)

const (
	unitTestAdminToken = "test-admin-token"
	unitTestMasterKey  = "test-master-key"
)

func TestAPIKeyHasherRoundTrip(t *testing.T) {
	t.Parallel()
	hasher := security.APIKeyHasher{}
	_, hash, err := hasher.Hash("ms-local-dev")
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}
	if !hasher.Verify("ms-local-dev", hash) {
		t.Fatal("expected verify to succeed")
	}
}

func TestEncryptorRoundTrip(t *testing.T) {
	t.Parallel()
	encryptor, err := security.NewAESGCMEncryptor(unitTestMasterKey)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	ciphertext, err := encryptor.Encrypt(context.Background(), []byte("secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plaintext, err := encryptor.Decrypt(context.Background(), ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("unexpected plaintext: %s", plaintext)
	}
}

func TestAdminSessionManager(t *testing.T) {
	t.Parallel()
	manager := security.NewAdminSessionManager(unitTestAdminToken, time.Hour)
	token := manager.Issue("admin")
	if !manager.Verify(token) {
		t.Fatal("expected issued session to verify")
	}
	if manager.Verify(token + "tamper") {
		t.Fatal("expected tampered session to fail")
	}
	if strings.Contains(token, "admin") {
		t.Fatal("expected session token to be encoded")
	}
}

func TestAdminAuthenticatorAcceptsOnlySessionCookieForHTTPAdmin(t *testing.T) {
	t.Parallel()

	manager := security.NewAdminSessionManager(unitTestAdminToken, time.Hour)
	auth := security.Authenticator{AdminToken: unitTestAdminToken, SessionManager: manager}

	bearerRequest := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	bearerRequest.Header.Set("Authorization", "Bearer "+unitTestAdminToken)
	if auth.AuthenticateAdmin(bearerRequest) {
		t.Fatal("expected admin HTTP auth to reject raw bearer admin tokens")
	}

	headerRequest := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	headerRequest.Header.Set("X-MemStation-Admin-Token", unitTestAdminToken)
	if auth.AuthenticateAdmin(headerRequest) {
		t.Fatal("expected admin HTTP auth to reject admin token headers")
	}

	invalidRequest := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	invalidRequest.Header.Set("Authorization", "Bearer wrong-token")
	if auth.AuthenticateAdmin(invalidRequest) {
		t.Fatal("expected admin HTTP auth to reject invalid bearer admin tokens")
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
	sessionRequest.AddCookie(&http.Cookie{Name: security.AdminSessionCookieName, Value: manager.Issue("admin")})
	if !auth.AuthenticateAdmin(sessionRequest) {
		t.Fatal("expected admin HTTP auth to accept a valid session cookie")
	}
}

func TestAdminTokenHashVerifiesWithoutRawTokenCredential(t *testing.T) {
	t.Parallel()

	hash, err := security.HashAdminToken(unitTestAdminToken)
	if err != nil {
		t.Fatalf("hash admin token: %v", err)
	}
	if strings.Contains(hash, unitTestAdminToken) {
		t.Fatal("expected admin token hash to avoid embedding the raw token")
	}

	auth := security.Authenticator{AdminToken: unitTestAdminToken}
	if !auth.AuthenticateAdminToken(hash) {
		t.Fatal("expected admin token exchange to accept an argon2id admin token hash")
	}

	if auth.AuthenticateAdminToken(strings.Replace(hash, "v=19", "v=18", 1)) {
		t.Fatal("expected tampered admin token hash to be rejected")
	}
}
