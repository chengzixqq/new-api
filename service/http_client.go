package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

const (
	upstreamIdleConnTimeout       = 90 * time.Second
	upstreamH2ReadIdleTimeout     = 15 * time.Second
	upstreamH2PingTimeout         = 5 * time.Second
	upstreamDialTimeout           = 30 * time.Second
	upstreamTLSHandshakeTimeout   = 10 * time.Second
	upstreamResponseHeaderTimeout = 300 * time.Second
	mediaResponseHeaderTimeout    = 30 * time.Second
	upstreamExpectContinueTimeout = time.Second
	upstreamTCPKeepAlive          = 30 * time.Second
)

var (
	httpClient              *http.Client
	httpClientH1            *http.Client
	mediaWorkerHTTPClient   *http.Client
	ssrfProtectedHTTPClient *http.Client
	proxyClientLock         sync.Mutex
	proxyClients            = make(map[string]*http.Client)
	proxyClientsH1          = make(map[string]*http.Client)
	mediaProxyClients       = make(map[string]*http.Client)
	legacyProxyURLWarnings  sync.Map
)

type proxyURLConfig struct {
	parsedURL *url.URL
	cacheKey  string
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	urlStr := req.URL.String()
	if err := validateURLWithCurrentFetchSetting(urlStr, true); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func checkProtectedFetchRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	// The protected RoundTripper resolves and validates every redirect target
	// exactly once before dialing it. Resolving here as well would reintroduce a
	// DNS time-of-check/time-of-use window.
	if req == nil || req.URL == nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return fmt.Errorf("redirect target uses an unsupported protocol")
	}
	return nil
}

func validateURLWithCurrentFetchSetting(urlStr string, applyDomainIPFilter bool) error {
	fetchSetting := system_setting.GetFetchSetting()
	return common.ValidateURLWithFetchSetting(urlStr, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, applyDomainIPFilter && fetchSetting.ApplyIPFilterForDomain)
}

func ValidateSSRFProtectedFetchURL(urlStr string) error {
	return validateURLWithCurrentFetchSetting(urlStr, true)
}

func InitHttpClient() error {
	if err := validateUpstreamTimeoutConfig(); err != nil {
		return err
	}

	transport, _, err := buildUpstreamTransport(http.ProxyFromEnvironment, nil)
	if err != nil {
		return fmt.Errorf("initialize HTTP/2 transport: %w", err)
	}
	httpClient = buildUpstreamHTTPClient(transport)
	mediaWorkerHTTPClient = buildMediaWorkerHTTPClient(transport)

	h1Transport := buildUpstreamTransportHTTP1Only(http.ProxyFromEnvironment, nil)
	httpClientH1 = buildUpstreamHTTPClient(h1Transport)

	// SSRF-protected client for user-controlled URL fetches (see GetSSRFProtectedHTTPClient).
	// Kept separate from the general upstream client so normal relay/provider traffic is
	// not forced through the SSRF dialer.
	ssrfProtectedHTTPClient = newProtectedFetchHTTPClient()
	return nil
}

func validateUpstreamTimeoutConfig() error {
	if common.RelayTimeout < 0 {
		return fmt.Errorf("RELAY_TIMEOUT must be greater than or equal to 0")
	}
	settings := []struct {
		name  string
		value int
	}{
		{name: "RELAY_DIAL_TIMEOUT", value: common.RelayDialTimeout},
		{name: "RELAY_TLS_HANDSHAKE_TIMEOUT", value: common.RelayTLSHandshakeTimeout},
		{name: "RELAY_RESPONSE_HEADER_TIMEOUT", value: common.RelayResponseHeaderTimeout},
	}
	for _, setting := range settings {
		if setting.value <= 0 {
			return fmt.Errorf("%s must be greater than 0", setting.name)
		}
	}
	return nil
}

func configuredUpstreamTimeout(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func buildUpstreamTLSConfig() *tls.Config {
	tlsConf := &tls.Config{
		ClientSessionCache: tls.NewLRUClientSessionCache(0),
	}
	if common.TLSInsecureSkipVerify {
		tlsConf.InsecureSkipVerify = true
	}
	return tlsConf
}

func buildUpstreamTransport(proxyFunc func(*http.Request) (*url.URL, error), dialContext func(context.Context, string, string) (net.Conn, error)) (*http.Transport, *http2.Transport, error) {
	idleConnTimeout := upstreamIdleConnTimeout
	if common.RelayIdleConnTimeout > 0 {
		idleConnTimeout = time.Duration(common.RelayIdleConnTimeout) * time.Second
	}
	if dialContext == nil {
		dialer := &net.Dialer{
			Timeout:   configuredUpstreamTimeout(common.RelayDialTimeout, upstreamDialTimeout),
			KeepAlive: upstreamTCPKeepAlive,
		}
		dialContext = dialer.DialContext
	}
	transport := &http.Transport{
		MaxIdleConns:          common.RelayMaxIdleConns,
		MaxIdleConnsPerHost:   common.RelayMaxIdleConnsPerHost,
		ForceAttemptHTTP2:     true,
		Proxy:                 proxyFunc,
		DialContext:           dialContext,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   configuredUpstreamTimeout(common.RelayTLSHandshakeTimeout, upstreamTLSHandshakeTimeout),
		ResponseHeaderTimeout: configuredUpstreamTimeout(common.RelayResponseHeaderTimeout, upstreamResponseHeaderTimeout),
		ExpectContinueTimeout: upstreamExpectContinueTimeout,
		TLSClientConfig:       buildUpstreamTLSConfig(),
	}
	h2t, err := enableUpstreamH2Keepalive(transport)
	return transport, h2t, err
}

func buildUpstreamTransportHTTP1Only(proxyFunc func(*http.Request) (*url.URL, error), dialContext func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	idleConnTimeout := upstreamIdleConnTimeout
	if common.RelayIdleConnTimeout > 0 {
		idleConnTimeout = time.Duration(common.RelayIdleConnTimeout) * time.Second
	}
	tlsConf := buildUpstreamTLSConfig()
	tlsConf.NextProtos = []string{"http/1.1"}
	if dialContext == nil {
		dialer := &net.Dialer{
			Timeout:   configuredUpstreamTimeout(common.RelayDialTimeout, upstreamDialTimeout),
			KeepAlive: upstreamTCPKeepAlive,
		}
		dialContext = dialer.DialContext
	}
	return &http.Transport{
		MaxIdleConns:          common.RelayMaxIdleConns,
		MaxIdleConnsPerHost:   common.RelayMaxIdleConnsPerHost,
		ForceAttemptHTTP2:     false,
		Proxy:                 proxyFunc,
		DialContext:           dialContext,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   configuredUpstreamTimeout(common.RelayTLSHandshakeTimeout, upstreamTLSHandshakeTimeout),
		ResponseHeaderTimeout: configuredUpstreamTimeout(common.RelayResponseHeaderTimeout, upstreamResponseHeaderTimeout),
		ExpectContinueTimeout: upstreamExpectContinueTimeout,
		TLSClientConfig:       tlsConf,
	}
}

func enableUpstreamH2Keepalive(transport *http.Transport) (*http2.Transport, error) {
	h2t, err := http2.ConfigureTransports(transport)
	if err != nil {
		common.SysError("upstream keepalive: ConfigureTransports failed: " + err.Error())
		return nil, err
	}
	h2t.ReadIdleTimeout = upstreamH2ReadIdleTimeout
	h2t.PingTimeout = upstreamH2PingTimeout
	return h2t, nil
}

func buildUpstreamHTTPClient(transport *http.Transport) *http.Client {
	return &http.Client{
		Transport:     transport,
		CheckRedirect: checkRedirect,
	}
}

func buildMediaWorkerHTTPClient(transport *http.Transport) *http.Client {
	mediaTransport := transport.Clone()
	mediaTransport.ResponseHeaderTimeout = mediaResponseHeaderTimeout
	return buildUpstreamHTTPClient(mediaTransport)
}

// GetHttpClient returns the general outbound client used by relay/provider
// integrations. Do not attach the SSRF-protected dialer here: provider base URLs
// are root/operator-managed deployment targets, not arbitrary user-controlled
// input, and may legitimately point at private networks, private-link endpoints,
// self-hosted services, or local proxies. Code paths that fetch arbitrary
// user-controlled URLs must use GetSSRFProtectedHTTPClient or
// ValidateSSRFProtectedFetchURL instead.
func GetHttpClient() *http.Client {
	return httpClient
}

func GetHttpClientHTTP1Only() *http.Client {
	return httpClientH1
}

func GetMediaWorkerHTTPClient() *http.Client {
	return mediaWorkerHTTPClient
}

// GetMediaHTTPClientWithProxy returns a provider client with the media-specific
// response-header timeout. The full body lifetime remains controlled by the
// request context so long, continuously flowing videos are not cut off.
func GetMediaHTTPClientWithProxy(proxyURL string) (*http.Client, error) {
	config, err := runtimeProxyURLConfig(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	if config == nil {
		if mediaWorkerHTTPClient == nil {
			return nil, fmt.Errorf("media HTTP client is not initialized")
		}
		return mediaWorkerHTTPClient, nil
	}

	proxyClientLock.Lock()
	if client, ok := mediaProxyClients[config.cacheKey]; ok {
		proxyClientLock.Unlock()
		return client, nil
	}
	proxyClientLock.Unlock()

	baseClient, err := GetHttpClientWithProxy(proxyURL)
	if err != nil {
		return nil, err
	}
	baseTransport, ok := baseClient.Transport.(*http.Transport)
	if !ok || baseTransport == nil {
		return nil, fmt.Errorf("media proxy client has an unsupported transport")
	}
	client := buildMediaWorkerHTTPClient(baseTransport)

	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	if cached, ok := mediaProxyClients[config.cacheKey]; ok {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
		return cached, nil
	}
	mediaProxyClients[config.cacheKey] = client
	return client, nil
}

// GetSSRFProtectedHTTPClient 返回带拨号时 SSRF 校验的客户端。
// ssrfProtectedHTTPClient 由 InitHttpClient 在启动时初始化，运行期只读。
func GetSSRFProtectedHTTPClient() *http.Client {
	return ssrfProtectedHTTPClient
}

func newProxyURLConfig(parsedURL *url.URL) *proxyURLConfig {
	return &proxyURLConfig{
		parsedURL: parsedURL,
		cacheKey:  parsedURL.String(),
	}
}

// ResetProxyClientCache 清空代理客户端缓存，确保下次使用时重新初始化
func ResetProxyClientCache() {
	proxyClientLock.Lock()
	for _, client := range proxyClients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	proxyClients = make(map[string]*http.Client)
	for _, client := range proxyClientsH1 {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	proxyClientsH1 = make(map[string]*http.Client)
	for _, client := range mediaProxyClients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	mediaProxyClients = make(map[string]*http.Client)
	proxyClientLock.Unlock()

	resetUpstreamHTTP2PoolCache()
}

func dialProxyContext(ctx context.Context, dialer proxy.Dialer, network, addr string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, network, addr)
	}

	type dialResult struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan dialResult)
	go func() {
		conn, err := dialer.Dial(network, addr)
		select {
		case resultCh <- dialResult{conn: conn, err: err}:
		case <-ctx.Done():
			if conn != nil {
				_ = conn.Close()
			}
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		return result.conn, result.err
	}
}

func warnLegacyProxyURLOnce(config *proxyURLConfig) {
	if _, loaded := legacyProxyURLWarnings.LoadOrStore(config.cacheKey, struct{}{}); loaded {
		return
	}
	logger.LogWarn(
		context.Background(),
		fmt.Sprintf(
			"legacy proxy URL suffix ignored at runtime: scheme=%s host=%s; update the channel proxy setting",
			config.parsedURL.Scheme,
			config.parsedURL.Host,
		),
	)
}

// NormalizeProxyURL validates a proxy URL using runtime-compatible rules and returns its canonical cache key.
func NormalizeProxyURL(rawProxyURL string) (string, error) {
	parsedURL, legacySuffixStripped, err := common.ParseProxyURLRuntime(rawProxyURL)
	if err != nil {
		return "", err
	}
	if parsedURL == nil {
		return "", nil
	}
	config := newProxyURLConfig(parsedURL)
	if legacySuffixStripped {
		warnLegacyProxyURLOnce(config)
	}
	return config.cacheKey, nil
}

// ValidateProxyURL validates a channel proxy URL without connecting to it.
func ValidateProxyURL(rawProxyURL string) error {
	_, err := common.ParseProxyURLStrict(rawProxyURL)
	return err
}

func runtimeProxyURLConfig(rawProxyURL string) (*proxyURLConfig, error) {
	parsedURL, legacySuffixStripped, err := common.ParseProxyURLRuntime(strings.TrimSpace(rawProxyURL))
	if err != nil {
		return nil, err
	}
	if parsedURL == nil {
		return nil, nil
	}
	config := newProxyURLConfig(parsedURL)
	if legacySuffixStripped {
		warnLegacyProxyURLOnce(config)
	}
	return config, nil
}

func buildProxyHTTPClient(parsedURL *url.URL, http1Only bool) (*http.Client, error) {
	var auth *proxy.Auth
	if parsedURL.User != nil {
		auth = &proxy.Auth{User: parsedURL.User.Username()}
		if password, ok := parsedURL.User.Password(); ok {
			auth.Password = password
		}
	}

	var dialContext func(context.Context, string, string) (net.Conn, error)
	var proxyFunc func(*http.Request) (*url.URL, error)
	switch parsedURL.Scheme {
	case "http", "https":
		proxyFunc = http.ProxyURL(parsedURL)
	case "socks5", "socks5h":
		forwardDialer := &net.Dialer{
			Timeout:   configuredUpstreamTimeout(common.RelayDialTimeout, upstreamDialTimeout),
			KeepAlive: upstreamTCPKeepAlive,
		}
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, forwardDialer)
		if err != nil {
			return nil, err
		}
		dialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialProxyContext(ctx, dialer, network, addr)
		}
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
	}

	if http1Only {
		return buildUpstreamHTTPClient(buildUpstreamTransportHTTP1Only(proxyFunc, dialContext)), nil
	}
	transport, _, err := buildUpstreamTransport(proxyFunc, dialContext)
	if err != nil {
		return nil, err
	}
	return buildUpstreamHTTPClient(transport), nil
}

// GetHttpClientWithProxy returns the default client or a cached proxy-enabled client.
func GetHttpClientWithProxy(rawProxyURL string) (*http.Client, error) {
	config, err := runtimeProxyURLConfig(rawProxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	if config == nil {
		if client := GetHttpClient(); client != nil {
			return client, nil
		}
		return http.DefaultClient, nil
	}

	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	if client := proxyClients[config.cacheKey]; client != nil {
		return client, nil
	}
	client, err := buildProxyHTTPClient(config.parsedURL, false)
	if err != nil {
		return nil, err
	}
	proxyClients[config.cacheKey] = client
	return client, nil
}

// NewProxyHttpClientHTTP1Only creates a cached HTTP/1.1-only proxy client.
func NewProxyHttpClientHTTP1Only(rawProxyURL string) (*http.Client, error) {
	config, err := runtimeProxyURLConfig(rawProxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	if config == nil {
		return GetHttpClientHTTP1Only(), nil
	}

	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	if client := proxyClientsH1[config.cacheKey]; client != nil {
		return client, nil
	}
	client, err := buildProxyHTTPClient(config.parsedURL, true)
	if err != nil {
		return nil, err
	}
	proxyClientsH1[config.cacheKey] = client
	return client, nil
}

// InvalidateProxyClient removes all cached client variants for one proxy.
func InvalidateProxyClient(rawProxyURL string) {
	config, err := runtimeProxyURLConfig(rawProxyURL)
	if err != nil || config == nil {
		return
	}

	proxyClientLock.Lock()
	clients := []*http.Client{
		proxyClients[config.cacheKey],
		proxyClientsH1[config.cacheKey],
		mediaProxyClients[config.cacheKey],
	}
	delete(proxyClients, config.cacheKey)
	delete(proxyClientsH1, config.cacheKey)
	delete(mediaProxyClients, config.cacheKey)
	proxyClientLock.Unlock()

	for _, client := range clients {
		if client != nil {
			client.CloseIdleConnections()
		}
	}
}

// NewProxyHttpClient is kept for compatibility.
// Deprecated: use GetHttpClientWithProxy.
func NewProxyHttpClient(proxyURL string) (*http.Client, error) {
	return GetHttpClientWithProxy(proxyURL)
}
