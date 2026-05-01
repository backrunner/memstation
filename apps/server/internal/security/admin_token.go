package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	AdminTokenHashPrefix = "$memstation-admin-argon2id$"

	adminTokenHashVersion     = 19
	adminTokenHashMemoryKiB   = 64 * 1024
	adminTokenHashIterations  = 1
	adminTokenHashParallelism = 4
	adminTokenHashSaltLength  = 16
	adminTokenHashKeyLength   = 32
)

func HashAdminToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("admin token is required")
	}
	salt := make([]byte, adminTokenHashSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	derived := argon2.IDKey([]byte(token), salt, adminTokenHashIterations, adminTokenHashMemoryKiB, adminTokenHashParallelism, adminTokenHashKeyLength)
	return fmt.Sprintf(
		"%sv=%d$m=%d,t=%d,p=%d$%s$%s",
		AdminTokenHashPrefix,
		adminTokenHashVersion,
		adminTokenHashMemoryKiB,
		adminTokenHashIterations,
		adminTokenHashParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived),
	), nil
}

func VerifyAdminToken(token, credential string) bool {
	token = strings.TrimSpace(token)
	credential = strings.TrimSpace(credential)
	if token == "" || credential == "" {
		return false
	}
	if strings.HasPrefix(credential, AdminTokenHashPrefix) {
		return verifyAdminTokenHash(token, credential)
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(credential)) == 1
}

func verifyAdminTokenHash(token, encoded string) bool {
	parts := strings.Split(strings.TrimPrefix(encoded, AdminTokenHashPrefix), "$")
	if len(parts) != 4 {
		return false
	}
	if parts[0] != "v="+strconv.Itoa(adminTokenHashVersion) {
		return false
	}
	memory, iterations, parallelism, ok := parseAdminTokenHashParams(parts[1])
	if !ok || memory == 0 || iterations == 0 || parallelism == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) == 0 {
		return false
	}
	derived := argon2.IDKey([]byte(token), salt, iterations, memory, uint8(parallelism), uint32(len(expected)))
	return subtle.ConstantTimeCompare(derived, expected) == 1
}

func parseAdminTokenHashParams(raw string) (uint32, uint32, uint32, bool) {
	values := map[string]uint32{}
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return 0, 0, 0, false
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return 0, 0, 0, false
		}
		values[key] = uint32(parsed)
	}
	return values["m"], values["t"], values["p"], true
}
