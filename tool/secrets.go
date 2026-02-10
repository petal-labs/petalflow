package tool

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
	secretEnvKeyPrefix   = "PETALFLOW_SECRET_KEY"
	encryptedValuePrefix = "enc:v1:"
)

type secretCodec struct {
	aead cipher.AEAD
}

func newSecretCodec(scope string) (*secretCodec, error) {
	key := deriveSecretKey(scope)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &secretCodec{aead: aead}, nil
}

func deriveSecretKey(scope string) []byte {
	if env := strings.TrimSpace(os.Getenv(secretEnvKeyPrefix)); env != "" {
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
	material := fmt.Sprintf("petalflow:%s:%s:%s", username, hostname, strings.TrimSpace(scope))
	sum := sha256.Sum256([]byte(material))
	return sum[:]
}

func (c *secretCodec) Encrypt(value string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("tool: secret codec is not initialized")
	}
	if strings.TrimSpace(value) == "" {
		return value, nil
	}
	if isEncryptedValue(value) {
		return value, nil
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(value), nil)
	payload := append(nonce, ciphertext...)
	return encryptedValuePrefix + base64.StdEncoding.EncodeToString(payload), nil
}

func (c *secretCodec) Decrypt(value string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("tool: secret codec is not initialized")
	}
	if !isEncryptedValue(value) {
		return value, nil
	}

	raw := strings.TrimPrefix(value, encryptedValuePrefix)
	payload, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}

	nonceSize := c.aead.NonceSize()
	if len(payload) < nonceSize {
		return "", fmt.Errorf("tool: encrypted payload is too short")
	}

	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func isEncryptedValue(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), encryptedValuePrefix)
}
