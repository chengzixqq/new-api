package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

const (
	UpstreamHTTPModeAuto   = "auto"
	UpstreamHTTPModeHTTP1  = "http1"
	UpstreamHTTPModeHybrid = "hybrid"

	minUpstreamHTTP2PoolSize = 1
	maxUpstreamHTTP2PoolSize = 64

	upstreamHTTP2PoolCacheLimit = 256
	upstreamHTTP2PoolIdleTTL    = 15 * time.Minute
	upstreamHTTP2PoolSweepEvery = time.Minute
)

// UpstreamHTTPClientOptions is the fully resolved transport policy for one
// provider request. Configuration precedence is intentionally resolved by the
// caller; this type only selects and tracks a concrete transport.
type UpstreamHTTPClientOptions struct {
	ChannelID               int
	ProxyURL                string
	Mode                    string
	HTTP2PoolSize           int
	HTTP1BodyThresholdBytes int64
	Method                  string
	HasBody                 bool
	ContentLength           int64
}

// UpstreamTransportSelection describes the decision made before dispatch. It
// contains no target URL or proxy data, so it is safe to expose in admin logs.
type UpstreamTransportSelection struct {
	Mode                     string
	HTTP2PoolSize            int
	HTTP2Shard               int
	ShardActiveAtPick        int64
	PendingUploadBytesAtPick int64
}

// UpstreamHTTPPoolStats is a process-local snapshot for the admin performance
// endpoint. Error keys are bounded protocol categories, never raw error text.
type UpstreamHTTPPoolStats struct {
	PoolCount          int               `json:"pool_count"`
	ShardCount         int               `json:"shard_count"`
	ActiveStreams      int64             `json:"active_streams"`
	PendingUploadBytes int64             `json:"pending_upload_bytes"`
	HTTP2Errors        map[string]uint64 `json:"http2_errors"`
}

type upstreamHTTP2PoolKey struct {
	channelID   int
	proxyDigest [sha256.Size]byte
	poolSize    int
}

type upstreamHTTP2Pool struct {
	key              upstreamHTTP2PoolKey
	shards           []*upstreamHTTP2Shard
	next             atomic.Uint64
	lastUsedUnixNano atomic.Int64
	warnedFallback   atomic.Bool
}

type upstreamHTTP2Shard struct {
	transport          *http.Transport
	activeStreams      atomic.Int64
	pendingUploadBytes atomic.Int64
}

type selectedUpstreamRoundTripper struct {
	pool         *upstreamHTTP2Pool
	shard        *upstreamHTTP2Shard
	channelID    int
	pendingBytes int64
}

type upstreamPolicyRoundTripper struct {
	options UpstreamHTTPClientOptions
}

// upstreamNoReplayRoundTripper keeps the original request replayable for
// explicit HTTP redirects, while hiding GetBody from the concrete transport.
// This prevents net/http and x/net/http2 from replaying a body-bearing provider
// request after a transport failure. Retries that happen before a usable
// connection accepts any request bytes, and bodyless idempotent requests, remain
// the standard library's safe behavior.
type upstreamNoReplayRoundTripper struct {
	base http.RoundTripper
}

type upstreamHTTPPolicyContextKey struct{}

type trackedUpstreamBody struct {
	io.ReadCloser
	release func()
}

type upstreamRequestLease struct {
	shard        *upstreamHTTP2Shard
	pendingBytes int64
	uploadOnce   sync.Once
	activeOnce   sync.Once
	stopMu       sync.Mutex
	stopContext  func() bool
	released     atomic.Bool
}

var upstreamHTTP2PoolCache = struct {
	sync.Mutex
	pools     map[upstreamHTTP2PoolKey]*upstreamHTTP2Pool
	byChannel map[int]upstreamHTTP2PoolKey
}{
	pools:     make(map[upstreamHTTP2PoolKey]*upstreamHTTP2Pool),
	byChannel: make(map[int]upstreamHTTP2PoolKey),
}

var upstreamHTTP2ErrorStats sync.Map // map[string]*atomic.Uint64
var upstreamHTTP2PoolSweepOnce sync.Once
var upstreamHTTP2PoolChannels sync.Map // set[int]struct{}, fast no-pool invalidation check

// WithChannelUpstreamHTTPPolicy stores a resolved channel policy on a polling
// context. Task adaptors keep their historical FetchTask signature and read the
// policy through NewUpstreamHTTPPolicyClientFromContext.
func WithChannelUpstreamHTTPPolicy(ctx context.Context, channelID int, settings dto.ChannelOtherSettings) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	resolved := model_setting.ResolveUpstreamHTTPConfig(settings)
	return context.WithValue(ctx, upstreamHTTPPolicyContextKey{}, UpstreamHTTPClientOptions{
		ChannelID:               channelID,
		Mode:                    string(resolved.Mode),
		HTTP2PoolSize:           resolved.HTTP2ConnectionPoolSize,
		HTTP1BodyThresholdBytes: int64(resolved.HTTP1BodyThresholdKiB) << 10,
	})
}

// NewUpstreamHTTPPolicyClientFromContext returns a provider client using the
// channel policy attached by the task polling caller. Missing context metadata
// preserves legacy auto/one-connection behavior.
func NewUpstreamHTTPPolicyClientFromContext(ctx context.Context, proxyURL string) *http.Client {
	options := UpstreamHTTPClientOptions{
		Mode:          UpstreamHTTPModeAuto,
		HTTP2PoolSize: minUpstreamHTTP2PoolSize,
	}
	if ctx != nil {
		if configured, ok := ctx.Value(upstreamHTTPPolicyContextKey{}).(UpstreamHTTPClientOptions); ok {
			options = configured
		}
	}
	options.ProxyURL = proxyURL
	return NewUpstreamHTTPPolicyClient(options)
}

// NewUpstreamHTTPPolicyClient returns an http.Client for integrations (such as
// provider SDKs) that construct their own requests. The policy is evaluated at
// RoundTrip time so hybrid mode can use the SDK-generated method and body size.
func NewUpstreamHTTPPolicyClient(options UpstreamHTTPClientOptions) *http.Client {
	return &http.Client{
		Transport:     &upstreamPolicyRoundTripper{options: options},
		CheckRedirect: checkRedirect,
	}
}

// NewChannelUpstreamHTTPPolicyClient resolves one channel's effective policy
// and returns a client suitable for provider SDKs and preparatory API calls.
func NewChannelUpstreamHTTPPolicyClient(channelID int, proxyURL string, settings dto.ChannelOtherSettings) *http.Client {
	resolved := model_setting.ResolveUpstreamHTTPConfig(settings)
	return NewUpstreamHTTPPolicyClient(UpstreamHTTPClientOptions{
		ChannelID:               channelID,
		ProxyURL:                proxyURL,
		Mode:                    string(resolved.Mode),
		HTTP2PoolSize:           resolved.HTTP2ConnectionPoolSize,
		HTTP1BodyThresholdBytes: int64(resolved.HTTP1BodyThresholdKiB) << 10,
	})
}

// SelectUpstreamHTTPClient chooses exactly one protocol/client before the
// request is sent. It never retries or falls back after RoundTrip starts.
func SelectUpstreamHTTPClient(options UpstreamHTTPClientOptions) (*http.Client, UpstreamTransportSelection, error) {
	mode := strings.ToLower(strings.TrimSpace(options.Mode))
	if mode == "" {
		mode = UpstreamHTTPModeAuto
	}
	if mode != UpstreamHTTPModeAuto && mode != UpstreamHTTPModeHTTP1 && mode != UpstreamHTTPModeHybrid {
		return nil, UpstreamTransportSelection{}, fmt.Errorf("unsupported upstream HTTP mode %q", mode)
	}
	selection := UpstreamTransportSelection{Mode: mode}
	if mode == UpstreamHTTPModeHTTP1 {
		invalidateUpstreamHTTP2PoolForChannel(options.ChannelID)
		client, err := getUpstreamHTTP1Client(options.ProxyURL)
		if err != nil {
			return nil, UpstreamTransportSelection{}, err
		}
		return newUpstreamNoReplayClient(client), selection, nil
	}

	poolSize := options.HTTP2PoolSize
	if poolSize == 0 {
		poolSize = minUpstreamHTTP2PoolSize
	}
	if poolSize < minUpstreamHTTP2PoolSize || poolSize > maxUpstreamHTTP2PoolSize {
		return nil, UpstreamTransportSelection{}, fmt.Errorf("HTTP/2 connection pool size must be between %d and %d", minUpstreamHTTP2PoolSize, maxUpstreamHTTP2PoolSize)
	}

	useHTTP1 := false
	if mode == UpstreamHTTPModeHybrid {
		threshold := options.HTTP1BodyThresholdBytes
		if threshold <= 0 {
			return nil, UpstreamTransportSelection{}, errors.New("hybrid upstream HTTP mode requires a positive body threshold")
		}
		method := strings.ToUpper(strings.TrimSpace(options.Method))
		switch {
		case method == http.MethodGet || method == http.MethodHead || !options.HasBody:
			useHTTP1 = false
		case options.ContentLength < 0:
			useHTTP1 = true
		default:
			useHTTP1 = options.ContentLength >= threshold
		}
	}

	if useHTTP1 {
		client, err := getUpstreamHTTP1Client(options.ProxyURL)
		if err != nil {
			return nil, UpstreamTransportSelection{}, err
		}
		return newUpstreamNoReplayClient(client), selection, nil
	}

	selection.HTTP2PoolSize = poolSize
	if poolSize == 1 {
		invalidateUpstreamHTTP2PoolForChannel(options.ChannelID)
		client, err := GetHttpClientWithProxy(options.ProxyURL)
		if err != nil {
			return nil, UpstreamTransportSelection{}, err
		}
		return newUpstreamNoReplayClient(client), selection, nil
	}

	pool, err := getOrCreateUpstreamHTTP2Pool(options.ChannelID, options.ProxyURL, poolSize)
	if err != nil {
		return nil, UpstreamTransportSelection{}, err
	}
	shardIndex, activeAtPick, pendingAtPick := pool.pickShard()
	selection.HTTP2Shard = shardIndex + 1
	selection.ShardActiveAtPick = activeAtPick
	selection.PendingUploadBytesAtPick = pendingAtPick

	pendingBytes := options.ContentLength
	if pendingBytes < 0 {
		pendingBytes = 0
	}
	client := &http.Client{
		Transport: &upstreamNoReplayRoundTripper{
			base: &selectedUpstreamRoundTripper{
				pool:         pool,
				shard:        pool.shards[shardIndex],
				channelID:    options.ChannelID,
				pendingBytes: pendingBytes,
			},
		},
		CheckRedirect: checkRedirect,
	}
	return client, selection, nil
}

func newUpstreamNoReplayClient(base *http.Client) *http.Client {
	if base == nil {
		return nil
	}
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &http.Client{
		Transport:     &upstreamNoReplayRoundTripper{base: transport},
		CheckRedirect: base.CheckRedirect,
		Jar:           base.Jar,
		Timeout:       base.Timeout,
	}
}

func getUpstreamHTTP1Client(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		if client := GetHttpClientHTTP1Only(); client != nil {
			return client, nil
		}
		return nil, errors.New("HTTP/1 upstream client is not initialized")
	}
	return NewProxyHttpClientHTTP1Only(proxyURL)
}

func getOrCreateUpstreamHTTP2Pool(channelID int, proxyURL string, poolSize int) (*upstreamHTTP2Pool, error) {
	key := upstreamHTTP2PoolKey{
		channelID:   channelID,
		proxyDigest: sha256.Sum256([]byte(proxyURL)),
		poolSize:    poolSize,
	}
	now := time.Now()

	upstreamHTTP2PoolCache.Lock()
	defer upstreamHTTP2PoolCache.Unlock()
	if pool := upstreamHTTP2PoolCache.pools[key]; pool != nil {
		pool.lastUsedUnixNano.Store(now.UnixNano())
		evictUpstreamHTTP2PoolsLocked(now, key)
		return pool, nil
	}

	pool, err := newUpstreamHTTP2Pool(key, proxyURL, now)
	if err != nil {
		return nil, err
	}
	if channelID != 0 {
		if oldKey, ok := upstreamHTTP2PoolCache.byChannel[channelID]; ok && oldKey != key {
			if oldPool := upstreamHTTP2PoolCache.pools[oldKey]; oldPool != nil {
				oldPool.closeIdleConnections()
				delete(upstreamHTTP2PoolCache.pools, oldKey)
			}
		}
		upstreamHTTP2PoolCache.byChannel[channelID] = key
		upstreamHTTP2PoolChannels.Store(channelID, struct{}{})
	}
	upstreamHTTP2PoolCache.pools[key] = pool
	evictUpstreamHTTP2PoolsLocked(now, key)
	startUpstreamHTTP2PoolSweeper()
	return pool, nil
}

func startUpstreamHTTP2PoolSweeper() {
	upstreamHTTP2PoolSweepOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(upstreamHTTP2PoolSweepEvery)
			defer ticker.Stop()
			for now := range ticker.C {
				upstreamHTTP2PoolCache.Lock()
				evictUpstreamHTTP2PoolsLocked(now, upstreamHTTP2PoolKey{})
				upstreamHTTP2PoolCache.Unlock()
			}
		}()
	})
}

func invalidateUpstreamHTTP2PoolForChannel(channelID int) {
	if channelID == 0 {
		return
	}
	if _, ok := upstreamHTTP2PoolChannels.Load(channelID); !ok {
		return
	}
	upstreamHTTP2PoolCache.Lock()
	if key, ok := upstreamHTTP2PoolCache.byChannel[channelID]; ok {
		if pool := upstreamHTTP2PoolCache.pools[key]; pool != nil {
			pool.closeIdleConnections()
			delete(upstreamHTTP2PoolCache.pools, key)
		}
		delete(upstreamHTTP2PoolCache.byChannel, channelID)
	}
	upstreamHTTP2PoolChannels.Delete(channelID)
	upstreamHTTP2PoolCache.Unlock()
}

func newUpstreamHTTP2Pool(key upstreamHTTP2PoolKey, proxyURL string, now time.Time) (*upstreamHTTP2Pool, error) {
	pool := &upstreamHTTP2Pool{
		key:    key,
		shards: make([]*upstreamHTTP2Shard, 0, key.poolSize),
	}
	pool.lastUsedUnixNano.Store(now.UnixNano())
	for index := 0; index < key.poolSize; index++ {
		transport, h2Transport, err := buildUpstreamTransportForProxy(proxyURL)
		if err != nil {
			pool.closeIdleConnections()
			return nil, fmt.Errorf("initialize HTTP/2 pool shard %d: %w", index+1, err)
		}
		h2Transport.StrictMaxConcurrentStreams = true
		pool.shards = append(pool.shards, &upstreamHTTP2Shard{transport: transport})
	}
	return pool, nil
}

func buildUpstreamTransportForProxy(proxyURL string) (*http.Transport, *http2.Transport, error) {
	if proxyURL == "" {
		return buildUpstreamTransport(http.ProxyFromEnvironment, nil)
	}
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, nil, errors.New("invalid proxy URL")
	}
	switch parsedURL.Scheme {
	case "http", "https":
		return buildUpstreamTransport(http.ProxyURL(parsedURL), nil)
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{User: parsedURL.User.Username()}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}
		forwardDialer := &net.Dialer{
			Timeout:   configuredUpstreamTimeout(common.RelayDialTimeout, upstreamDialTimeout),
			KeepAlive: upstreamTCPKeepAlive,
		}
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, forwardDialer)
		if err != nil {
			return nil, nil, err
		}
		return buildUpstreamTransport(nil, func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialProxyContext(ctx, dialer, network, addr)
		})
	default:
		return nil, nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
	}
}

func (p *upstreamHTTP2Pool) pickShard() (index int, active int64, pending int64) {
	p.lastUsedUnixNano.Store(time.Now().UnixNano())
	start := int(p.next.Add(1)-1) % len(p.shards)
	bestIndex := start
	bestPending := p.shards[start].pendingUploadBytes.Load()
	bestActive := p.shards[start].activeStreams.Load()
	for offset := 1; offset < len(p.shards); offset++ {
		candidateIndex := (start + offset) % len(p.shards)
		candidate := p.shards[candidateIndex]
		candidatePending := candidate.pendingUploadBytes.Load()
		candidateActive := candidate.activeStreams.Load()
		if candidatePending < bestPending || (candidatePending == bestPending && candidateActive < bestActive) {
			bestIndex = candidateIndex
			bestPending = candidatePending
			bestActive = candidateActive
		}
	}
	return bestIndex, bestActive, bestPending
}

func (t *selectedUpstreamRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("upstream request is nil")
	}
	pendingBytes := t.pendingBytes
	if pendingBytes == 0 && req.ContentLength > 0 {
		pendingBytes = req.ContentLength
	}
	t.shard.activeStreams.Add(1)
	if pendingBytes > 0 {
		t.shard.pendingUploadBytes.Add(pendingBytes)
	}
	lease := &upstreamRequestLease{shard: t.shard, pendingBytes: pendingBytes}
	lease.setContextStop(context.AfterFunc(req.Context(), lease.releaseActive))

	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) {
		lease.releaseUpload()
	}}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := t.shard.transport.RoundTrip(req)
	if err != nil {
		lease.releaseActive()
		recordUpstreamHTTP2Error(err)
		return nil, err
	}
	if resp == nil {
		lease.releaseActive()
		return nil, errors.New("upstream transport returned a nil response")
	}
	if resp.ProtoMajor != 2 && t.pool.warnedFallback.CompareAndSwap(false, true) {
		logger.LogWarn(req.Context(), fmt.Sprintf("upstream HTTP/2 pool negotiated %s for channel %d; warning suppressed for this pool generation", resp.Proto, t.channelID))
	}
	if resp.Body == nil {
		lease.releaseActive()
		return resp, nil
	}
	resp.Body = &trackedUpstreamBody{ReadCloser: resp.Body, release: lease.releaseActive}
	return resp, nil
}

func (t *upstreamNoReplayRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("upstream request is nil")
	}
	if t == nil || t.base == nil {
		return nil, errors.New("upstream transport is not initialized")
	}

	transportRequest := req.Clone(req.Context())
	if transportRequest.Body != nil && transportRequest.Body != http.NoBody {
		transportRequest.GetBody = nil
	}
	return t.base.RoundTrip(transportRequest)
}

func (t *upstreamPolicyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("upstream request is nil")
	}
	options := t.options
	options.Method = req.Method
	options.HasBody = req.Body != nil && req.Body != http.NoBody
	options.ContentLength = req.ContentLength
	client, selection, err := SelectUpstreamHTTPClient(options)
	if err != nil {
		return nil, err
	}
	if client == nil || client.Transport == nil {
		return nil, errors.New("selected upstream HTTP client is not initialized")
	}
	response, err := client.Transport.RoundTrip(req)
	if err != nil {
		// Sharded transports account for their own errors at the concrete shard.
		// Shared HTTP/2-preferred clients need to be recorded here.
		if selection.HTTP2PoolSize == 1 {
			recordUpstreamHTTP2Error(err)
		}
		return nil, err
	}
	return response, nil
}

func (l *upstreamRequestLease) releaseUpload() {
	l.uploadOnce.Do(func() {
		if l.pendingBytes > 0 {
			l.shard.pendingUploadBytes.Add(-l.pendingBytes)
		}
	})
}

func (l *upstreamRequestLease) releaseActive() {
	l.activeOnce.Do(func() {
		l.releaseUpload()
		l.shard.activeStreams.Add(-1)
		l.released.Store(true)
	})
	l.stopMu.Lock()
	if l.stopContext != nil {
		l.stopContext()
		l.stopContext = nil
	}
	l.stopMu.Unlock()
}

func (l *upstreamRequestLease) setContextStop(stop func() bool) {
	l.stopMu.Lock()
	if l.released.Load() {
		stop()
	} else {
		l.stopContext = stop
	}
	l.stopMu.Unlock()
}

func (b *trackedUpstreamBody) Read(buffer []byte) (int, error) {
	n, err := b.ReadCloser.Read(buffer)
	if err != nil {
		b.release()
	}
	return n, err
}

func (b *trackedUpstreamBody) Close() error {
	err := b.ReadCloser.Close()
	b.release()
	return err
}

func recordUpstreamHTTP2Error(err error) {
	category := "other"
	var streamError http2.StreamError
	var goAwayError http2.GoAwayError
	var connectionError http2.ConnectionError
	switch {
	case errors.As(err, &streamError):
		category = upstreamHTTP2ErrorCodeCategory("stream", streamError.Code)
	case errors.As(err, &goAwayError):
		category = upstreamHTTP2ErrorCodeCategory("goaway", goAwayError.ErrCode)
	case errors.As(err, &connectionError):
		category = upstreamHTTP2ErrorCodeCategory("connection", http2.ErrCode(connectionError))
	case errors.Is(err, context.Canceled):
		category = "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		category = "context_deadline"
	}
	counter, _ := upstreamHTTP2ErrorStats.LoadOrStore(category, &atomic.Uint64{})
	counter.(*atomic.Uint64).Add(1)
}

func upstreamHTTP2ErrorCodeCategory(prefix string, code http2.ErrCode) string {
	name := strings.ToLower(code.String())
	if strings.HasPrefix(name, "unknown error code") {
		name = "unknown"
	}
	return prefix + "_" + name
}

// RecordUpstreamHTTP2Error records a categorized error for code paths that use
// the shared (pool size one) HTTP/2-preferred client directly.
func RecordUpstreamHTTP2Error(err error) {
	if err != nil {
		recordUpstreamHTTP2Error(err)
	}
}

func GetUpstreamHTTPPoolStats() UpstreamHTTPPoolStats {
	stats := UpstreamHTTPPoolStats{HTTP2Errors: make(map[string]uint64)}
	upstreamHTTP2PoolCache.Lock()
	stats.PoolCount = len(upstreamHTTP2PoolCache.pools)
	for _, pool := range upstreamHTTP2PoolCache.pools {
		stats.ShardCount += len(pool.shards)
		for _, shard := range pool.shards {
			stats.ActiveStreams += shard.activeStreams.Load()
			stats.PendingUploadBytes += shard.pendingUploadBytes.Load()
		}
	}
	upstreamHTTP2PoolCache.Unlock()
	upstreamHTTP2ErrorStats.Range(func(key, value any) bool {
		stats.HTTP2Errors[key.(string)] = value.(*atomic.Uint64).Load()
		return true
	})
	return stats
}

func evictUpstreamHTTP2PoolsLocked(now time.Time, keep upstreamHTTP2PoolKey) {
	for key, pool := range upstreamHTTP2PoolCache.pools {
		if key == keep {
			continue
		}
		if pool.hasActiveStreams() {
			continue
		}
		lastUsed := time.Unix(0, pool.lastUsedUnixNano.Load())
		if now.Sub(lastUsed) >= upstreamHTTP2PoolIdleTTL {
			pool.closeIdleConnections()
			delete(upstreamHTTP2PoolCache.pools, key)
			if current, ok := upstreamHTTP2PoolCache.byChannel[key.channelID]; ok && current == key {
				delete(upstreamHTTP2PoolCache.byChannel, key.channelID)
				upstreamHTTP2PoolChannels.Delete(key.channelID)
			}
		}
	}
	for len(upstreamHTTP2PoolCache.pools) > upstreamHTTP2PoolCacheLimit {
		var oldestKey upstreamHTTP2PoolKey
		var oldestPool *upstreamHTTP2Pool
		for key, pool := range upstreamHTTP2PoolCache.pools {
			if key == keep {
				continue
			}
			if oldestPool == nil || pool.lastUsedUnixNano.Load() < oldestPool.lastUsedUnixNano.Load() {
				oldestKey, oldestPool = key, pool
			}
		}
		if oldestPool == nil {
			break
		}
		oldestPool.closeIdleConnections()
		delete(upstreamHTTP2PoolCache.pools, oldestKey)
		if current, ok := upstreamHTTP2PoolCache.byChannel[oldestKey.channelID]; ok && current == oldestKey {
			delete(upstreamHTTP2PoolCache.byChannel, oldestKey.channelID)
			upstreamHTTP2PoolChannels.Delete(oldestKey.channelID)
		}
	}
}

func (p *upstreamHTTP2Pool) hasActiveStreams() bool {
	for _, shard := range p.shards {
		if shard != nil && shard.activeStreams.Load() > 0 {
			return true
		}
	}
	return false
}

func (p *upstreamHTTP2Pool) closeIdleConnections() {
	for _, shard := range p.shards {
		if shard != nil && shard.transport != nil {
			shard.transport.CloseIdleConnections()
		}
	}
}

func resetUpstreamHTTP2PoolCache() {
	upstreamHTTP2PoolCache.Lock()
	for _, pool := range upstreamHTTP2PoolCache.pools {
		pool.closeIdleConnections()
	}
	upstreamHTTP2PoolCache.pools = make(map[upstreamHTTP2PoolKey]*upstreamHTTP2Pool)
	upstreamHTTP2PoolCache.byChannel = make(map[int]upstreamHTTP2PoolKey)
	upstreamHTTP2PoolChannels.Range(func(key, _ any) bool {
		upstreamHTTP2PoolChannels.Delete(key)
		return true
	})
	upstreamHTTP2PoolCache.Unlock()
}
