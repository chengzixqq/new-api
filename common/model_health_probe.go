package common

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
)

const (
	ModelHealthProbeHeader        = "X-New-Api-Health-Probe"
	modelHealthProbeHeaderVersion = "v1"
	modelHealthProbeMaxClockSkew  = 2 * time.Minute
)

// NewModelHealthProbeHeader signs the method and path of a local full-chain
// probe. The receiving middleware removes this header before relay forwarding
// and marks the request only after VerifyModelHealthProbeHeader succeeds.
func NewModelHealthProbeHeader(method, path string, now time.Time) (string, error) {
	nonce, err := GenerateRandomCharsKey(20)
	if err != nil {
		return "", err
	}
	timestamp := now.Unix()
	payload := modelHealthProbePayload(timestamp, nonce, method, path)
	signature := GenerateHMACWithKey(modelHealthProbeHMACKey(), payload)
	return fmt.Sprintf("%s.%d.%s.%s", modelHealthProbeHeaderVersion, timestamp, nonce, signature), nil
}

func VerifyModelHealthProbeHeader(value, method, path string, now time.Time) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != modelHealthProbeHeaderVersion || parts[2] == "" {
		return false
	}
	timestamp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	delta := now.Sub(time.Unix(timestamp, 0))
	if delta < -modelHealthProbeMaxClockSkew || delta > modelHealthProbeMaxClockSkew {
		return false
	}
	expected := GenerateHMACWithKey(
		modelHealthProbeHMACKey(),
		modelHealthProbePayload(timestamp, parts[2], method, path),
	)
	return hmac.Equal([]byte(expected), []byte(parts[3]))
}

// WithModelHealthProbeContext propagates the verified marker through the
// standard request context used by relay adaptors and error handlers.
func WithModelHealthProbeContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, constant.ContextKeyHealthProbe, true)
}

func IsModelHealthProbeContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(constant.ContextKeyHealthProbe).(bool)
	return value
}

func modelHealthProbePayload(timestamp int64, nonce, method, path string) string {
	return fmt.Sprintf("%d\n%s\n%s\n%s", timestamp, nonce, strings.ToUpper(method), path)
}

func modelHealthProbeHMACKey() []byte {
	sum := sha256.Sum256([]byte("new-api/model-health/probe/v1\x00" + CryptoSecret))
	return sum[:]
}
