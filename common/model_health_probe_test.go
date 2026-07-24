package common

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelHealthProbeHeaderBindsMethodPathAndTimestamp(t *testing.T) {
	original := CryptoSecret
	CryptoSecret = "stable-test-secret"
	t.Cleanup(func() { CryptoSecret = original })
	now := time.Unix(1_700_000_000, 0)

	header, err := NewModelHealthProbeHeader("POST", "/v1/chat/completions", now)
	require.NoError(t, err)
	assert.True(t, VerifyModelHealthProbeHeader(header, "POST", "/v1/chat/completions", now.Add(time.Minute)))
	assert.False(t, VerifyModelHealthProbeHeader(header, "GET", "/v1/chat/completions", now))
	assert.False(t, VerifyModelHealthProbeHeader(header, "POST", "/v1/responses", now))
	assert.False(t, VerifyModelHealthProbeHeader(header, "POST", "/v1/chat/completions", now.Add(3*time.Minute)))
}

func TestModelHealthProbeHeaderRejectsTampering(t *testing.T) {
	original := CryptoSecret
	CryptoSecret = "stable-test-secret"
	t.Cleanup(func() { CryptoSecret = original })
	now := time.Unix(1_700_000_000, 0)
	header, err := NewModelHealthProbeHeader("POST", "/v1/messages", now)
	require.NoError(t, err)

	assert.False(t, VerifyModelHealthProbeHeader(header+"0", "POST", "/v1/messages", now))
	assert.False(t, VerifyModelHealthProbeHeader("malformed", "POST", "/v1/messages", now))
}

func TestModelHealthProbeContextPropagation(t *testing.T) {
	assert.False(t, IsModelHealthProbeContext(context.Background()))
	ctx := WithModelHealthProbeContext(context.Background())
	assert.True(t, IsModelHealthProbeContext(ctx))
}
