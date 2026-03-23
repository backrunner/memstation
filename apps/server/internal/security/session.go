package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const AdminSessionCookieName = "memstation_admin_session"

type AdminSessionManager struct {
	secret []byte
	ttl    time.Duration
}

func NewAdminSessionManager(secret string, ttl time.Duration) *AdminSessionManager {
	sum := sha256.Sum256([]byte(secret))
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &AdminSessionManager{secret: sum[:], ttl: ttl}
}

func (m *AdminSessionManager) Issue(subject string) string {
	if subject == "" {
		subject = "admin"
	}
	expiresAt := time.Now().UTC().Add(m.ttl).Unix()
	payload := subject + "|" + strconv.FormatInt(expiresAt, 10)
	signature := m.sign(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + signature))
}

func (m *AdminSessionManager) Verify(token string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return false
	}
	payload := strings.Join(parts[:2], "|")
	if !hmac.Equal([]byte(parts[2]), []byte(m.sign(payload))) {
		return false
	}
	expiresAt, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	return time.Now().UTC().Unix() <= expiresAt
}

func (m *AdminSessionManager) MaxAge() int {
	return int(m.ttl / time.Second)
}

func (m *AdminSessionManager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func RedactBearerToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return token
	}
	return fmt.Sprintf("%s...%s", token[:4], token[len(token)-4:])
}
