package baidu

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaiduExpiredTokenRefreshUsesSingleflight(t *testing.T) {
	const cacheKey = "channel-1:credentials"
	baiduTokenStore.Delete(cacheKey)
	baiduTokenRefreshing.Delete(cacheKey)
	originalFetcher := baiduAccessTokenFetcher
	t.Cleanup(func() {
		baiduAccessTokenFetcher = originalFetcher
		baiduTokenStore.Delete(cacheKey)
		baiduTokenRefreshing.Delete(cacheKey)
	})

	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	baiduAccessTokenFetcher = func(context.Context, string, string) (*BaiduAccessToken, error) {
		calls.Add(1)
		startOnce.Do(func() { close(started) })
		<-release
		now := time.Now()
		return &BaiduAccessToken{AccessToken: "fresh", ExpiresAt: now.Add(time.Hour), RefreshAt: now.Add(50 * time.Minute)}, nil
	}

	const workers = 16
	results := make(chan string, workers)
	errorsCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			token, err := getBaiduAccessToken(context.Background(), cacheKey, "client|secret", "")
			results <- token
			errorsCh <- err
		}()
	}
	<-started
	close(release)
	for i := 0; i < workers; i++ {
		require.NoError(t, <-errorsCh)
		assert.Equal(t, "fresh", <-results)
	}
	assert.Equal(t, int32(1), calls.Load())
}

func TestBaiduNearExpiryTokenRefreshesAsynchronouslyOnce(t *testing.T) {
	const cacheKey = "channel-2:credentials"
	now := time.Now()
	baiduTokenStore.Store(cacheKey, BaiduAccessToken{
		AccessToken: "current",
		ExpiresAt:   now.Add(time.Hour),
		RefreshAt:   now.Add(-time.Minute),
	})
	baiduTokenRefreshing.Delete(cacheKey)
	originalFetcher := baiduAccessTokenFetcher
	t.Cleanup(func() {
		baiduAccessTokenFetcher = originalFetcher
		baiduTokenStore.Delete(cacheKey)
		baiduTokenRefreshing.Delete(cacheKey)
	})

	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	baiduAccessTokenFetcher = func(context.Context, string, string) (*BaiduAccessToken, error) {
		calls.Add(1)
		startOnce.Do(func() { close(started) })
		<-release
		refreshedAt := time.Now()
		return &BaiduAccessToken{AccessToken: "refreshed", ExpiresAt: refreshedAt.Add(time.Hour), RefreshAt: refreshedAt.Add(50 * time.Minute)}, nil
	}

	for i := 0; i < 20; i++ {
		token, err := getBaiduAccessToken(context.Background(), cacheKey, "client|secret", "")
		require.NoError(t, err)
		assert.Equal(t, "current", token)
	}
	<-started
	assert.Equal(t, int32(1), calls.Load())
	close(release)
	require.Eventually(t, func() bool {
		value, ok := baiduTokenStore.Load(cacheKey)
		if !ok {
			return false
		}
		token, ok := value.(BaiduAccessToken)
		_, refreshing := baiduTokenRefreshing.Load(cacheKey)
		return ok && token.AccessToken == "refreshed" && !refreshing
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, int32(1), calls.Load())
}

func TestBaiduExpiredTokenRefreshInheritsRequestCancellation(t *testing.T) {
	const cacheKey = "channel-3:credentials"
	baiduTokenStore.Delete(cacheKey)
	originalFetcher := baiduAccessTokenFetcher
	t.Cleanup(func() {
		baiduAccessTokenFetcher = originalFetcher
		baiduTokenStore.Delete(cacheKey)
	})
	baiduAccessTokenFetcher = func(ctx context.Context, _, _ string) (*BaiduAccessToken, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := getBaiduAccessToken(ctx, cacheKey, "client|secret", "")

	require.ErrorIs(t, err, context.Canceled)
}
