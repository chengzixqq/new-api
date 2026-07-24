package common

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadHTTPServerConfigDefaults(t *testing.T) {
	for _, name := range []string{
		"HTTP_SERVER_READ_HEADER_TIMEOUT_SECONDS",
		"HTTP_SERVER_READ_TIMEOUT_SECONDS",
		"HTTP_SERVER_IDLE_TIMEOUT_SECONDS",
		"HTTP_SERVER_MAX_HEADER_BYTES",
	} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}

	config, err := LoadHTTPServerConfig()

	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, config.ReadHeaderTimeout)
	assert.Equal(t, 300*time.Second, config.ReadTimeout)
	assert.Zero(t, config.WriteTimeout)
	assert.Equal(t, 120*time.Second, config.IdleTimeout)
	assert.Equal(t, 256<<10, config.MaxHeaderBytes)
}

func TestNewHTTPServerAppliesValidatedBoundaries(t *testing.T) {
	handler := http.NewServeMux()
	config := HTTPServerConfig{
		ReadHeaderTimeout: 11 * time.Second,
		ReadTimeout:       301 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       121 * time.Second,
		MaxHeaderBytes:    300 << 10,
	}

	server := NewHTTPServer(":3000", handler, config)

	assert.Equal(t, ":3000", server.Addr)
	assert.Same(t, handler, server.Handler)
	assert.Equal(t, config.ReadHeaderTimeout, server.ReadHeaderTimeout)
	assert.Equal(t, config.ReadTimeout, server.ReadTimeout)
	assert.Zero(t, server.WriteTimeout)
	assert.Equal(t, config.IdleTimeout, server.IdleTimeout)
	assert.Equal(t, config.MaxHeaderBytes, server.MaxHeaderBytes)
}

func TestLoadHTTPServerAddressValidatesPort(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("PORT", "")
		address, port, err := LoadHTTPServerAddress(3000)
		require.NoError(t, err)
		assert.Equal(t, ":3000", address)
		assert.Equal(t, "3000", port)
	})

	t.Run("override", func(t *testing.T) {
		t.Setenv("PORT", " 8080 ")
		address, port, err := LoadHTTPServerAddress(3000)
		require.NoError(t, err)
		assert.Equal(t, ":8080", address)
		assert.Equal(t, "8080", port)
	})

	for _, value := range []string{"invalid", "0", "65536"} {
		t.Run("reject "+value, func(t *testing.T) {
			t.Setenv("PORT", value)
			_, _, err := LoadHTTPServerAddress(3000)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "PORT")
		})
	}
}

func TestLoadHTTPServerConfigOverrides(t *testing.T) {
	t.Setenv("HTTP_SERVER_READ_HEADER_TIMEOUT_SECONDS", "15")
	t.Setenv("HTTP_SERVER_READ_TIMEOUT_SECONDS", "600")
	t.Setenv("HTTP_SERVER_IDLE_TIMEOUT_SECONDS", "240")
	t.Setenv("HTTP_SERVER_MAX_HEADER_BYTES", "524288")

	config, err := LoadHTTPServerConfig()

	require.NoError(t, err)
	assert.Equal(t, 15*time.Second, config.ReadHeaderTimeout)
	assert.Equal(t, 600*time.Second, config.ReadTimeout)
	assert.Zero(t, config.WriteTimeout)
	assert.Equal(t, 240*time.Second, config.IdleTimeout)
	assert.Equal(t, 512<<10, config.MaxHeaderBytes)
}

func TestLoadHTTPServerConfigRejectsInvalidValues(t *testing.T) {
	testCases := []struct {
		name  string
		env   string
		value string
	}{
		{name: "empty", env: "HTTP_SERVER_READ_HEADER_TIMEOUT_SECONDS", value: " "},
		{name: "not integer", env: "HTTP_SERVER_READ_TIMEOUT_SECONDS", value: "five"},
		{name: "read header disabled", env: "HTTP_SERVER_READ_HEADER_TIMEOUT_SECONDS", value: "0"},
		{name: "read header too large", env: "HTTP_SERVER_READ_HEADER_TIMEOUT_SECONDS", value: "301"},
		{name: "read timeout too large", env: "HTTP_SERVER_READ_TIMEOUT_SECONDS", value: "3601"},
		{name: "idle timeout disabled", env: "HTTP_SERVER_IDLE_TIMEOUT_SECONDS", value: "0"},
		{name: "headers too small", env: "HTTP_SERVER_MAX_HEADER_BYTES", value: "16383"},
		{name: "headers too large", env: "HTTP_SERVER_MAX_HEADER_BYTES", value: "1048577"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(testCase.env, testCase.value)

			_, err := LoadHTTPServerConfig()

			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.env)
		})
	}
}
