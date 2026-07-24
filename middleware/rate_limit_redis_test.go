package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
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

func TestRedisEmailVerificationRateLimitUsesSlidingWindow(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	require.NoError(t, redisClient.Ping(context.Background()).Err())
	common.RedisEnabled = true
	common.RDB = redisClient
	t.Cleanup(func() {
		_ = redisClient.Close()
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/verify", EmailVerificationRateLimit(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/verify", nil)
		req.RemoteAddr = "192.0.2.30:12345"
		router.ServeHTTP(recorder, req)
		return recorder
	}

	assert.Equal(t, http.StatusNoContent, request().Code)
	assert.Equal(t, http.StatusNoContent, request().Code)
	response := request()
	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Equal(t, "30", response.Header().Get("Retry-After"))
	assert.JSONEq(t, `{"success":false,"message":"发送过于频繁，请等待 30 秒后再试"}`, response.Body.String())

	key := redisIPRateLimitKey(EmailVerificationRateLimitMark, "192.0.2.30")
	count, err := redisClient.ZCard(context.Background(), key).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(EmailVerificationMaxRequests), count)
	assert.Greater(t, redisServer.TTL(key), time.Duration(EmailVerificationDuration)*time.Second)
	assert.LessOrEqual(t, redisServer.TTL(key), time.Duration(EmailVerificationDuration+1)*time.Second)
}
