package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisRateLimitKeysAreIsolatedFromLegacyLists(t *testing.T) {
	t.Parallel()

	ipKey := redisIPRateLimitKey("GA", "2001:db8::1")
	userKey := redisUserRateLimitKey("SR", 42)

	assert.Equal(t, "rateLimit:sw:GA:ip:2001:db8::1", ipKey)
	assert.Equal(t, "rateLimit:sw:SR:user:42", userKey)
	assert.NotEqual(t, "rateLimit:GA2001:db8::1", ipKey)
	assert.NotEqual(t, "rateLimit:SR:user:42", userKey)
}

func TestApplyRedisRateLimitUnlimitedDoesNotAccessRedis(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/", nil)

	require.NotPanics(t, func() {
		applyRedisRateLimit(c, "unused", 0, 60)
	})
	assert.False(t, c.IsAborted())
	assert.Empty(t, recorder.Header().Get("Retry-After"))
}
