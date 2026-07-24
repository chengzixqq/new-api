package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

const (
	modelHealthSecretVersion    = "v1"
	modelHealthSecretAAD        = "new-api/model-health/secret/v1"
	modelHealthMaxSecretBytes   = 8 * 1024
	modelHealthMaxEnvelopeBytes = 16 * 1024
)

var errModelHealthStableSecretRequired = errors.New("a stable CRYPTO_SECRET is required")

func ModelHealthStableSecretConfigured() bool {
	_, ok := modelHealthStableSecret()
	return ok
}

// EncryptModelHealthSecret creates a versioned AES-256-GCM envelope. The key
// is domain-separated from the configured application secret so ciphertext is
// not coupled to another encryption or signing use of that secret.
func EncryptModelHealthSecret(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("model health secret is empty")
	}
	if len(plaintext) > modelHealthMaxSecretBytes {
		return "", errors.New("model health secret is too large")
	}
	key, err := modelHealthEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), []byte(modelHealthSecretAAD))
	envelope := append(nonce, sealed...)
	return modelHealthSecretVersion + ":" + base64.RawURLEncoding.EncodeToString(envelope), nil
}

func DecryptModelHealthSecret(envelope string) (string, error) {
	if len(envelope) > modelHealthMaxEnvelopeBytes {
		return "", errors.New("invalid model health secret envelope")
	}
	parts := strings.SplitN(envelope, ":", 2)
	if len(parts) != 2 || parts[0] != modelHealthSecretVersion {
		return "", errors.New("unsupported model health secret envelope")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("invalid model health secret envelope")
	}
	key, err := modelHealthEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(encoded) <= gcm.NonceSize() {
		return "", errors.New("invalid model health secret envelope")
	}
	plaintext, err := gcm.Open(nil, encoded[:gcm.NonceSize()], encoded[gcm.NonceSize():], []byte(modelHealthSecretAAD))
	if err != nil {
		return "", errors.New("model health secret decryption failed")
	}
	return string(plaintext), nil
}

func ModelHealthCredentialFingerprint(apiKey string) string {
	sum := sha256.Sum256([]byte("new-api/model-health/fingerprint/v1\x00" + apiKey))
	return fmt.Sprintf("%x", sum[:8])
}

// MaskModelHealthEndpoint keeps enough host information for a root operator
// to identify a target while hiding user info, query parameters, fragments,
// and the full upstream path.
func MaskModelHealthEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "***"
	}
	host := parsed.Hostname()
	if len(host) > 4 {
		host = host[:2] + strings.Repeat("*", min(8, len(host)-4)) + host[len(host)-2:]
	} else {
		host = "***"
	}
	port := parsed.Port()
	if port != "" {
		host += ":" + port
	}
	return parsed.Scheme + "://" + host + "/..."
}

func modelHealthStableSecret() (string, bool) {
	value := strings.TrimSpace(os.Getenv("CRYPTO_SECRET"))
	return value, value != ""
}

func modelHealthEncryptionKey() ([]byte, error) {
	secret, ok := modelHealthStableSecret()
	if !ok {
		return nil, errModelHealthStableSecretRequired
	}
	sum := sha256.Sum256([]byte("new-api/model-health/encryption/v1\x00" + secret))
	return sum[:], nil
}
