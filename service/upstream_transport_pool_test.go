package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

func TestSelectUpstreamHTTPClientHybridBoundary(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)

	tests := []struct {
		name          string
		method        string
		hasBody       bool
		contentLength int64
		wantHTTP1     bool
	}{
		{name: "GET remains HTTP2 even with body", method: http.MethodGet, hasBody: true, contentLength: 4096},
		{name: "HEAD remains HTTP2", method: http.MethodHead, contentLength: -1},
		{name: "no body remains HTTP2", method: http.MethodPost},
		{name: "small known body remains HTTP2", method: http.MethodPost, hasBody: true, contentLength: 1023},
		{name: "threshold is HTTP1", method: http.MethodPost, hasBody: true, contentLength: 1024, wantHTTP1: true},
		{name: "unknown body is HTTP1", method: http.MethodPost, hasBody: true, contentLength: -1, wantHTTP1: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, selection, err := SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{
				Mode:                    UpstreamHTTPModeHybrid,
				HTTP2PoolSize:           1,
				HTTP1BodyThresholdBytes: 1024,
				Method:                  tt.method,
				HasBody:                 tt.hasBody,
				ContentLength:           tt.contentLength,
			})
			require.NoError(t, err)
			if tt.wantHTTP1 {
				wrapped, ok := client.Transport.(*upstreamNoReplayRoundTripper)
				require.True(t, ok)
				assert.Same(t, GetHttpClientHTTP1Only().Transport, wrapped.base)
				assert.Zero(t, selection.HTTP2PoolSize)
				return
			}
			wrapped, ok := client.Transport.(*upstreamNoReplayRoundTripper)
			require.True(t, ok)
			assert.Same(t, GetHttpClient().Transport, wrapped.base)
			assert.Equal(t, 1, selection.HTTP2PoolSize)
		})
	}
}

func TestSelectUpstreamHTTPClientValidatesRelevantPolicyFields(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)

	_, _, err := SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{Mode: "adaptive", HTTP2PoolSize: 1})
	require.ErrorContains(t, err, "unsupported upstream HTTP mode")

	_, _, err = SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{Mode: UpstreamHTTPModeAuto, HTTP2PoolSize: 65})
	require.ErrorContains(t, err, "pool size")

	_, _, err = SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{Mode: UpstreamHTTPModeHybrid, HTTP2PoolSize: 1})
	require.ErrorContains(t, err, "positive body threshold")

	client, selection, err := SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{
		Mode:                    UpstreamHTTPModeHTTP1,
		HTTP2PoolSize:           maxUpstreamHTTP2PoolSize + 1,
		HTTP1BodyThresholdBytes: -1,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, UpstreamHTTPModeHTTP1, selection.Mode)
	assert.Zero(t, selection.HTTP2PoolSize)
}

func TestNoReplayRoundTripperHidesGetBodyFromConcreteTransport(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/infer", bytes.NewBufferString("request"))
	require.NoError(t, err)
	require.NotNil(t, request.GetBody)

	var transportSawGetBody bool
	transport := &upstreamNoReplayRoundTripper{base: roundTripFunc(func(received *http.Request) (*http.Response, error) {
		transportSawGetBody = received.GetBody != nil
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    received,
		}, nil
	})}

	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.False(t, transportSawGetBody, "the concrete transport must not be able to replay a body-bearing provider request")
	assert.NotNil(t, request.GetBody, "redirect handling on the outer HTTP client must remain available")
}

func TestHTTP2ErrorCategoryBoundsUnknownCodes(t *testing.T) {
	assert.Equal(t, "stream_internal_error", upstreamHTTP2ErrorCodeCategory("stream", http2.ErrCodeInternal))
	assert.Equal(t, "goaway_unknown", upstreamHTTP2ErrorCodeCategory("goaway", http2.ErrCode(0xffff)))
}

func TestPolicyClientHybridChoosesProtocolFromActualRequestBody(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)
	resetUpstreamHTTP2PoolCache()

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("X-Observed-Protocol", request.Proto)
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	mode := dto.UpstreamHTTPModeHybrid
	client := NewChannelUpstreamHTTPPolicyClient(100, "", dto.ChannelOtherSettings{
		UpstreamHTTPMode:        &mode,
		HTTP2ConnectionPoolSize: common.GetPointer(2),
		HTTP1BodyThresholdKiB:   common.GetPointer(64),
	})

	for _, test := range []struct {
		name      string
		bodyBytes int
		wantProto string
	}{
		{name: "small uses HTTP2", bodyBytes: (64 << 10) - 1, wantProto: "HTTP/2.0"},
		{name: "threshold uses HTTP1", bodyBytes: 64 << 10, wantProto: "HTTP/1.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(make([]byte, test.bodyBytes)))
			require.NoError(t, err)
			response, err := client.Do(request)
			require.NoError(t, err)
			assert.Equal(t, test.wantProto, response.Header.Get("X-Observed-Protocol"))
			_, err = io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
		})
	}
}

func TestHTTP2PoolUsesIndependentConnectionsAndRoundRobinTies(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)
	resetUpstreamHTTP2PoolCache()

	const shardCount = 4
	allArrived := make(chan struct{})
	releaseResponses := make(chan struct{})
	var arrivedOnce sync.Once
	var mu sync.Mutex
	remoteAddresses := make(map[string]struct{})
	arrived := 0
	wrongProtocol := false
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		mu.Lock()
		wrongProtocol = wrongProtocol || request.ProtoMajor != 2
		remoteAddresses[request.RemoteAddr] = struct{}{}
		arrived++
		if arrived == shardCount {
			arrivedOnce.Do(func() { close(allArrived) })
		}
		mu.Unlock()
		<-releaseResponses
		_, _ = io.WriteString(w, "ok")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	clients := make([]*http.Client, 0, shardCount)
	for index := 0; index < shardCount; index++ {
		client, selection, err := SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{
			ChannelID:     101,
			Mode:          UpstreamHTTPModeAuto,
			HTTP2PoolSize: shardCount,
			Method:        http.MethodPost,
			HasBody:       true,
			ContentLength: 128,
		})
		require.NoError(t, err)
		assert.Equal(t, index+1, selection.HTTP2Shard)
		clients = append(clients, client)
	}

	errCh := make(chan error, shardCount)
	for _, client := range clients {
		go func(client *http.Client) {
			request, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(make([]byte, 128)))
			if err != nil {
				errCh <- err
				return
			}
			response, err := client.Do(request)
			if err == nil {
				_, err = io.ReadAll(response.Body)
				_ = response.Body.Close()
			}
			errCh <- err
		}(client)
	}
	<-allArrived
	stats := GetUpstreamHTTPPoolStats()
	assert.Equal(t, 1, stats.PoolCount)
	assert.Equal(t, shardCount, stats.ShardCount)
	assert.EqualValues(t, shardCount, stats.ActiveStreams)
	assert.Zero(t, stats.PendingUploadBytes)
	mu.Lock()
	assert.Len(t, remoteAddresses, shardCount)
	assert.False(t, wrongProtocol)
	mu.Unlock()

	close(releaseResponses)
	for index := 0; index < shardCount; index++ {
		require.NoError(t, <-errCh)
	}
	stats = GetUpstreamHTTPPoolStats()
	assert.Zero(t, stats.ActiveStreams)
	assert.Zero(t, stats.PendingUploadBytes)
}

func TestHTTP2PoolReleasesCountersOnEOFAndRepeatedClose(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)
	resetUpstreamHTTP2PoolCache()

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "response")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, _, err := SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{
		ChannelID:     102,
		Mode:          UpstreamHTTPModeAuto,
		HTTP2PoolSize: 2,
		Method:        http.MethodPost,
		HasBody:       true,
		ContentLength: 7,
	})
	require.NoError(t, err)
	request, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewBufferString("request"))
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	require.Equal(t, "response", mustReadString(t, response.Body))
	require.NoError(t, response.Body.Close())
	require.NoError(t, response.Body.Close())

	stats := GetUpstreamHTTPPoolStats()
	assert.Zero(t, stats.ActiveStreams)
	assert.Zero(t, stats.PendingUploadBytes)
}

func TestHTTP2PoolRedirectKeepsCountersBalanced(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)
	resetUpstreamHTTP2PoolCache()

	var finalRequests atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/start" {
			http.Redirect(w, request, "/final", http.StatusTemporaryRedirect)
			return
		}
		finalRequests.Add(1)
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, _, err := SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{
		ChannelID:     106,
		Mode:          UpstreamHTTPModeAuto,
		HTTP2PoolSize: 2,
		Method:        http.MethodPost,
		HasBody:       true,
		ContentLength: 7,
	})
	require.NoError(t, err)
	client.CheckRedirect = nil
	request, err := http.NewRequest(http.MethodPost, server.URL+"/start", bytes.NewBufferString("request"))
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	assert.Equal(t, "ok", mustReadString(t, response.Body))
	require.NoError(t, response.Body.Close())
	assert.EqualValues(t, 1, finalRequests.Load())
	assert.Zero(t, GetUpstreamHTTPPoolStats().ActiveStreams)
}

func TestHTTP2PoolDoesNotReplayPOSTAfterTransportError(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)
	resetUpstreamHTTP2PoolCache()

	var requests atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		panic(http.ErrAbortHandler)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, _, err := SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{
		ChannelID:     103,
		Mode:          UpstreamHTTPModeAuto,
		HTTP2PoolSize: 2,
		Method:        http.MethodPost,
		HasBody:       true,
		ContentLength: 7,
	})
	require.NoError(t, err)
	request, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewBufferString("request"))
	require.NoError(t, err)
	_, err = client.Do(request)
	require.Error(t, err)
	assert.EqualValues(t, 1, requests.Load())
	assert.Zero(t, GetUpstreamHTTPPoolStats().ActiveStreams)
}

func TestHTTP2PoolChannelConfigReplacementClosesOldGeneration(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)
	resetUpstreamHTTP2PoolCache()

	first, err := getOrCreateUpstreamHTTP2Pool(104, "", 2)
	require.NoError(t, err)
	second, err := getOrCreateUpstreamHTTP2Pool(104, "", 3)
	require.NoError(t, err)
	require.NotSame(t, first, second)

	stats := GetUpstreamHTTPPoolStats()
	assert.Equal(t, 1, stats.PoolCount)
	assert.Equal(t, 3, stats.ShardCount)
}

func TestHTTP2PoolIdleEvictionPreservesActiveStreams(t *testing.T) {
	resetUpstreamHTTP2PoolCache()
	t.Cleanup(resetUpstreamHTTP2PoolCache)

	now := time.Now()
	key := upstreamHTTP2PoolKey{channelID: 107, poolSize: 2}
	pool := &upstreamHTTP2Pool{
		key: key,
		shards: []*upstreamHTTP2Shard{
			{},
			{},
		},
	}
	pool.lastUsedUnixNano.Store(now.Add(-upstreamHTTP2PoolIdleTTL).UnixNano())
	pool.shards[0].activeStreams.Store(1)

	upstreamHTTP2PoolCache.Lock()
	upstreamHTTP2PoolCache.pools[key] = pool
	upstreamHTTP2PoolCache.byChannel[key.channelID] = key
	upstreamHTTP2PoolChannels.Store(key.channelID, struct{}{})
	evictUpstreamHTTP2PoolsLocked(now, upstreamHTTP2PoolKey{})
	_, remainsActive := upstreamHTTP2PoolCache.pools[key]
	upstreamHTTP2PoolCache.Unlock()
	assert.True(t, remainsActive, "an active SSE stream must keep its pool generation observable and reusable")

	pool.shards[0].activeStreams.Store(0)
	upstreamHTTP2PoolCache.Lock()
	evictUpstreamHTTP2PoolsLocked(now, upstreamHTTP2PoolKey{})
	_, remainsIdle := upstreamHTTP2PoolCache.pools[key]
	upstreamHTTP2PoolCache.Unlock()
	assert.False(t, remainsIdle)
}

func initializeUpstreamHTTPClientsForTest(t *testing.T) {
	t.Helper()
	previousInsecure := common.TLSInsecureSkipVerify
	previousDial := common.RelayDialTimeout
	previousTLS := common.RelayTLSHandshakeTimeout
	previousHeader := common.RelayResponseHeaderTimeout
	common.TLSInsecureSkipVerify = true
	common.RelayDialTimeout = 30
	common.RelayTLSHandshakeTimeout = 10
	common.RelayResponseHeaderTimeout = 300
	require.NoError(t, InitHttpClient())
	t.Cleanup(func() {
		resetUpstreamHTTP2PoolCache()
		common.TLSInsecureSkipVerify = previousInsecure
		common.RelayDialTimeout = previousDial
		common.RelayTLSHandshakeTimeout = previousTLS
		common.RelayResponseHeaderTimeout = previousHeader
	})
}

func mustReadString(t *testing.T, reader io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(data)
}

func TestHTTP2PoolContextCancellationReleasesCounters(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)
	resetUpstreamHTTP2PoolCache()

	headersWritten := make(chan struct{})
	requestCanceled := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(headersWritten)
		<-request.Context().Done()
		close(requestCanceled)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	client, _, err := SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{
		ChannelID:     105,
		Mode:          UpstreamHTTPModeAuto,
		HTTP2PoolSize: 2,
		Method:        http.MethodPost,
		HasBody:       true,
		ContentLength: 7,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewBufferString("request"))
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	<-headersWritten
	assert.EqualValues(t, 1, GetUpstreamHTTPPoolStats().ActiveStreams)
	cancel()
	<-requestCanceled
	_ = response.Body.Close()
	assert.Zero(t, GetUpstreamHTTPPoolStats().ActiveStreams)
}

func TestChannelWarmupBuildsEveryHTTP2ShardAndHybridHTTP1(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)
	resetUpstreamHTTP2PoolCache()

	autoMode := dto.UpstreamHTTPModeAuto
	autoChannel := &model.Channel{Id: 201}
	autoChannel.SetOtherSettings(dto.ChannelOtherSettings{
		UpstreamHTTPMode:        &autoMode,
		HTTP2ConnectionPoolSize: common.GetPointer(4),
	})
	autoTargets := makeChannelWarmupTargets(autoChannel, "https://upstream.example/v1/models", make(map[string]bool))
	require.Len(t, autoTargets, 4)
	for _, target := range autoTargets {
		assert.Equal(t, 1, target.attempts)
		assert.Contains(t, target.key, "h2-shard=")
	}

	hybridMode := dto.UpstreamHTTPModeHybrid
	hybridChannel := &model.Channel{Id: 202}
	hybridChannel.SetOtherSettings(dto.ChannelOtherSettings{
		UpstreamHTTPMode:        &hybridMode,
		HTTP2ConnectionPoolSize: common.GetPointer(2),
	})
	hybridTargets := makeChannelWarmupTargets(hybridChannel, "https://upstream.example/v1/models", make(map[string]bool))
	require.Len(t, hybridTargets, 3)
	assert.Contains(t, hybridTargets[2].key, "http1")
}
