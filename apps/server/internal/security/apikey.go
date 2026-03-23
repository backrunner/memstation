package security

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
)

const apiKeyPrefixLength = 8

type APIKeyHasher struct{}

func (APIKeyHasher) Hash(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", errors.New("api key is required")
	}
	sum := sha256.Sum256([]byte(trimmed))
	prefix := trimmed
	if len(prefix) > apiKeyPrefixLength {
		prefix = prefix[:apiKeyPrefixLength]
	}
	return prefix, hex.EncodeToString(sum[:]), nil
}

func (APIKeyHasher) Verify(raw, expectedHash string) bool {
	_, hashed, err := (APIKeyHasher{}).Hash(raw)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashed), []byte(expectedHash)) == 1
}

func DeriveUserIDFromAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "user_" + hex.EncodeToString(sum[:8])
}
