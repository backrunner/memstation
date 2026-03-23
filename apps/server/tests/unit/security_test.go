package unit_test

import (
	"context"
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
