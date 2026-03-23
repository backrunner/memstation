package security

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

type AESGCMEncryptor struct {
	key []byte
}

func NewAESGCMEncryptor(masterKey string) (*AESGCMEncryptor, error) {
	if masterKey == "" {
		return nil, errors.New("master key is required")
	}
	sum := sha256.Sum256([]byte(masterKey))
	return &AESGCMEncryptor{key: sum[:]}, nil
}

func (e *AESGCMEncryptor) Encrypt(_ context.Context, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (e *AESGCMEncryptor) Decrypt(_ context.Context, ciphertext string) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	nonceSize := aead.NonceSize()
	if len(raw) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, payload := raw[:nonceSize], raw[nonceSize:]
	return aead.Open(nil, nonce, payload, nil)
}
