package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOptionalEnvIntInRange(t *testing.T) {
	const name = "NEW_API_TEST_OPTIONAL_RANGE"

	t.Setenv(name, "")
	value, err := getOptionalEnvIntInRange(name, 1, 128)
	require.NoError(t, err)
	assert.Zero(t, value)

	t.Setenv(name, " 64 ")
	value, err = getOptionalEnvIntInRange(name, 1, 128)
	require.NoError(t, err)
	assert.Equal(t, 64, value)

	for _, invalid := range []string{"0", "129", "not-a-number"} {
		t.Setenv(name, invalid)
		_, err = getOptionalEnvIntInRange(name, 1, 128)
		require.Error(t, err)
	}
}

func TestGetOptionalUpstreamHTTPModeEnv(t *testing.T) {
	t.Setenv("RELAY_UPSTREAM_HTTP_MODE", "")
	value, err := getOptionalUpstreamHTTPModeEnv()
	require.NoError(t, err)
	assert.Empty(t, value)

	t.Setenv("RELAY_UPSTREAM_HTTP_MODE", " Hybrid ")
	value, err = getOptionalUpstreamHTTPModeEnv()
	require.NoError(t, err)
	assert.Equal(t, "hybrid", value)

	t.Setenv("RELAY_UPSTREAM_HTTP_MODE", "http3")
	_, err = getOptionalUpstreamHTTPModeEnv()
	require.Error(t, err)
}
