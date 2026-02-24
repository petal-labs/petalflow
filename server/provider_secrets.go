package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
)

const (
	providerSecretEnvKeyPrefix   = "PETALFLOW_SECRET_KEY"
	providerEncryptedValuePrefix = "enc:v1:"
)

type providerSecretCodec struct {
	aead cipher.AEAD
}

func newProviderSecretCodec(scope string) (*providerSecretCodec, error) {
	key := deriveProviderSecretKey(scope)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &providerSecretCodec{aead: aead}, nil
}

func deriveProviderSecretKey(scope string) []byte {
	if env := strings.TrimSpace(os.Getenv(providerSecretEnvKeyPrefix)); env != "" {
		if decoded, err := base64.StdEncoding.DecodeString(env); err == nil && len(decoded) > 0 {
			sum := sha256.Sum256(decoded)
			return sum[:]
		}
		sum := sha256.Sum256([]byte(env))
		return sum[:]
	}

	username := "unknown"
	if current, err := user.Current(); err == nil && current != nil {
		username = current.Username
	}
	hostname, _ := os.Hostname()
	material := fmt.Sprintf("petalflow:provider:%s:%s:%s", username, hostname, strings.TrimSpace(scope))
	sum := sha256.Sum256([]byte(material))
	return sum[:]
}

func (c *providerSecretCodec) Encrypt(value string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("provider: secret codec is not initialized")
	}
	if strings.TrimSpace(value) == "" {
		return value, nil
	}
	if isEncryptedProviderValue(value) {
		return value, nil
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(value), nil)
	payload := append(nonce, ciphertext...)
	return providerEncryptedValuePrefix + base64.StdEncoding.EncodeToString(payload), nil
}

func (c *providerSecretCodec) Decrypt(value string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("provider: secret codec is not initialized")
	}
	if !isEncryptedProviderValue(value) {
		return value, nil
	}

	raw := strings.TrimPrefix(value, providerEncryptedValuePrefix)
	payload, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}

	nonceSize := c.aead.NonceSize()
	if len(payload) < nonceSize {
		return "", fmt.Errorf("provider: encrypted payload is too short")
	}

	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func isEncryptedProviderValue(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), providerEncryptedValuePrefix)
}
