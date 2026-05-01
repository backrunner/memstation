package security

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type Authenticator struct {
	AdminToken     string
	DevAPIKey      string
	SessionManager *AdminSessionManager
	LookupAPIKey   func(context.Context, string, string) (string, error)
}

func (a Authenticator) AuthenticateAPIKey(ctx context.Context, r *http.Request) (string, error) {
	return a.AuthenticateAPIKeyToken(ctx, bearerToken(r.Header.Get("Authorization")))
}

func (a Authenticator) AuthenticateAPIKeyToken(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("missing api key")
	}
	if a.DevAPIKey != "" && token == a.DevAPIKey {
		return DeriveUserIDFromAPIKey(token), nil
	}
	if strings.HasPrefix(token, "ms-") && a.LookupAPIKey != nil {
		hasher := APIKeyHasher{}
		prefix, hash, err := hasher.Hash(token)
		if err != nil {
			return "", err
		}
		userID, err := a.LookupAPIKey(ctx, prefix, hash)
		if err != nil {
			return "", err
		}
		if userID != "" {
			return userID, nil
		}
	}
	return "", errors.New("invalid api key")
}

func (a Authenticator) AuthenticateAdmin(r *http.Request) bool {
	return a.AuthenticateAdminSession(r)
}

func (a Authenticator) AuthenticateAdminSession(r *http.Request) bool {
	cookie, err := r.Cookie(AdminSessionCookieName)
	return err == nil && a.SessionManager != nil && a.SessionManager.Verify(cookie.Value)
}

func (a Authenticator) AuthenticateAdminToken(token string) bool {
	return VerifyAdminToken(a.AdminToken, token)
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}
