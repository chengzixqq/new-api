package common

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetTrustedProxiesDefaults(t *testing.T) {
	originalValue, wasConfigured := os.LookupEnv("TRUSTED_PROXIES")
	require.NoError(t, os.Unsetenv("TRUSTED_PROXIES"))
	t.Cleanup(func() {
		if wasConfigured {
			require.NoError(t, os.Setenv("TRUSTED_PROXIES", originalValue))
			return
		}
		require.NoError(t, os.Unsetenv("TRUSTED_PROXIES"))
	})

	proxies, err := GetTrustedProxies()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"127.0.0.0/8",
		"::1/128",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}, proxies)
}

func TestGetTrustedProxiesExplicitConfiguration(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", " 127.0.0.1, 2001:db8::/32 ")

	proxies, err := GetTrustedProxies()
	require.NoError(t, err)
	assert.Equal(t, []string{"127.0.0.1", "2001:db8::/32"}, proxies)
}

func TestGetTrustedProxiesNoneDisablesForwardedHeaders(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "NONE")

	proxies, err := GetTrustedProxies()
	require.NoError(t, err)
	assert.Nil(t, proxies)
	assert.Equal(t, "10.0.0.2", clientIPForTest(t, proxies, "10.0.0.2:1234", "198.51.100.8"))
}

func TestGetTrustedProxiesRejectsInvalidConfiguration(t *testing.T) {
	testCases := []string{
		"",
		"example.com",
		"10.0.0.0/8,,127.0.0.1",
		"none,127.0.0.1",
	}

	for _, value := range testCases {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXIES", value)
			_, err := GetTrustedProxies()
			assert.Error(t, err)
		})
	}
}

func TestTrustedProxyClientIPBoundary(t *testing.T) {
	proxies := []string{
		"127.0.0.0/8",
		"::1/128",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}

	assert.Equal(t, "203.0.113.10", clientIPForTest(t, proxies, "203.0.113.10:1234", "198.51.100.8"))
	assert.Equal(t, "198.51.100.8", clientIPForTest(t, proxies, "10.0.0.2:1234", "198.51.100.8"))
}

func clientIPForTest(t *testing.T, proxies []string, remoteAddr string, forwardedFor string) string {
	t.Helper()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(proxies))

	clientIP := ""
	engine.GET("/", func(c *gin.Context) {
		clientIP = c.ClientIP()
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = remoteAddr
	request.Header.Set("X-Forwarded-For", forwardedFor)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	return clientIP
}
