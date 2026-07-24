package service

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingProxyDialer struct {
	started chan struct{}
	release chan struct{}
}

func (d *blockingProxyDialer) Dial(_, _ string) (net.Conn, error) {
	close(d.started)
	<-d.release
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}

func TestBuildUpstreamTransportConfig_AllPaths(t *testing.T) {
	httpProxy, err := url.Parse("http://proxy.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, context.Canceled
	}

	tests := []struct {
		name        string
		proxy       func(*http.Request) (*url.URL, error)
		dialContext func(context.Context, string, string) (net.Conn, error)
	}{
		{name: "no proxy", proxy: http.ProxyFromEnvironment},
		{name: "http proxy", proxy: http.ProxyURL(httpProxy)},
		{name: "socks5 proxy", dialContext: dialContext},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, h2t, err := buildUpstreamTransport(tt.proxy, tt.dialContext)
			if err != nil {
				t.Fatalf("buildUpstreamTransport returned error: %v", err)
			}
			if transport.IdleConnTimeout != upstreamIdleConnTimeout {
				t.Fatalf("IdleConnTimeout = %s, want %s", transport.IdleConnTimeout, upstreamIdleConnTimeout)
			}
			if transport.TLSClientConfig == nil {
				t.Fatal("TLSClientConfig is nil")
			}
			if transport.TLSClientConfig.ClientSessionCache == nil {
				t.Fatal("ClientSessionCache is nil")
			}
			if !containsString(transport.TLSClientConfig.NextProtos, "h2") {
				t.Fatalf("NextProtos = %#v, want h2", transport.TLSClientConfig.NextProtos)
			}
			if !containsString(transport.TLSClientConfig.NextProtos, "http/1.1") {
				t.Fatalf("NextProtos = %#v, want http/1.1", transport.TLSClientConfig.NextProtos)
			}
			if h2t == nil {
				t.Fatal("h2 transport is nil")
			}
			if h2t.ReadIdleTimeout != upstreamH2ReadIdleTimeout {
				t.Fatalf("ReadIdleTimeout = %s, want %s", h2t.ReadIdleTimeout, upstreamH2ReadIdleTimeout)
			}
			if h2t.PingTimeout != upstreamH2PingTimeout {
				t.Fatalf("PingTimeout = %s, want %s", h2t.PingTimeout, upstreamH2PingTimeout)
			}
		})
	}
}

func TestBuildMediaWorkerHTTPClientUsesMediaHeaderTimeout(t *testing.T) {
	baseTransport := &http.Transport{ResponseHeaderTimeout: upstreamResponseHeaderTimeout}

	client := buildMediaWorkerHTTPClient(baseTransport)

	mediaTransport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotSame(t, baseTransport, mediaTransport)
	require.Equal(t, mediaResponseHeaderTimeout, mediaTransport.ResponseHeaderTimeout)
	require.Equal(t, upstreamResponseHeaderTimeout, baseTransport.ResponseHeaderTimeout)
}

func TestBuildUpstreamTransport_UsesStageTimeoutsWithoutWholeRequestTimeout(t *testing.T) {
	originalDial := common.RelayDialTimeout
	originalTLS := common.RelayTLSHandshakeTimeout
	originalHeader := common.RelayResponseHeaderTimeout
	originalLegacy := common.RelayTimeout
	common.RelayDialTimeout = 7
	common.RelayTLSHandshakeTimeout = 8
	common.RelayResponseHeaderTimeout = 9
	common.RelayTimeout = 1
	t.Cleanup(func() {
		common.RelayDialTimeout = originalDial
		common.RelayTLSHandshakeTimeout = originalTLS
		common.RelayResponseHeaderTimeout = originalHeader
		common.RelayTimeout = originalLegacy
	})

	transport, _, err := buildUpstreamTransport(nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 8*time.Second, transport.TLSHandshakeTimeout)
	assert.Equal(t, 9*time.Second, transport.ResponseHeaderTimeout)
	assert.Equal(t, time.Second, transport.ExpectContinueTimeout)
	assert.NotNil(t, transport.DialContext)

	client := buildUpstreamHTTPClient(transport)
	assert.Zero(t, client.Timeout, "legacy RELAY_TIMEOUT must not truncate long-lived streams")
}

func TestValidateUpstreamTimeoutConfigRejectsNonPositiveValues(t *testing.T) {
	originalDial := common.RelayDialTimeout
	originalTLS := common.RelayTLSHandshakeTimeout
	originalHeader := common.RelayResponseHeaderTimeout
	originalLegacy := common.RelayTimeout
	t.Cleanup(func() {
		common.RelayDialTimeout = originalDial
		common.RelayTLSHandshakeTimeout = originalTLS
		common.RelayResponseHeaderTimeout = originalHeader
		common.RelayTimeout = originalLegacy
	})

	common.RelayDialTimeout = 30
	common.RelayTLSHandshakeTimeout = 10
	common.RelayResponseHeaderTimeout = 300
	common.RelayTimeout = 0
	require.NoError(t, validateUpstreamTimeoutConfig())

	tests := []struct {
		name   string
		mutate func()
	}{
		{name: "negative legacy timeout", mutate: func() { common.RelayTimeout = -1 }},
		{name: "zero dial timeout", mutate: func() { common.RelayDialTimeout = 0 }},
		{name: "zero TLS timeout", mutate: func() { common.RelayTLSHandshakeTimeout = 0 }},
		{name: "zero response header timeout", mutate: func() { common.RelayResponseHeaderTimeout = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			common.RelayDialTimeout = 30
			common.RelayTLSHandshakeTimeout = 10
			common.RelayResponseHeaderTimeout = 300
			common.RelayTimeout = 0
			test.mutate()
			require.Error(t, validateUpstreamTimeoutConfig())
		})
	}
}

func TestDialProxyContext_CancelStopsNonContextDialer(t *testing.T) {
	dialer := &blockingProxyDialer{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() {
		_, err := dialProxyContext(ctx, dialer, "tcp", "example.com:443")
		resultCh <- err
	}()

	select {
	case <-dialer.started:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy dial did not start")
	}
	cancel()
	select {
	case err := <-resultCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("proxy dial did not stop after context cancellation")
	}
	close(dialer.release)
}

func TestNewProxyHttpClient_ConcurrentFirstCallReturnsSameClient(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	const proxyURL = "http://proxy.example:8080"
	var wg sync.WaitGroup
	start := make(chan struct{})
	clients := make([]*http.Client, 32)
	errs := make([]error, len(clients))

	for i := range clients {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			clients[i], errs[i] = NewProxyHttpClient(proxyURL)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("client %d returned error: %v", i, err)
		}
	}
	first := clients[0]
	if first == nil {
		t.Fatal("first client is nil")
	}
	for i, client := range clients {
		if client != first {
			t.Fatalf("client %d pointer differs: got %p want %p", i, client, first)
		}
	}
}

func TestBuildUpstreamTLSConfig_AllowsSessionResumption(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	tlsConf := buildUpstreamTLSConfig()
	tlsConf.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	transport := &http.Transport{
		TLSClientConfig:   tlsConf,
		ForceAttemptHTTP2: true,
	}
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	_ = resp.Body.Close()
	transport.CloseIdleConnections()

	var didResume bool
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			didResume = state.DidResume
		},
	}))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	_ = resp.Body.Close()

	if !didResume {
		t.Fatal("second TLS handshake did not resume")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestBuildUpstreamTLSConfig_DoesNotMutateGlobalInsecureConfig(t *testing.T) {
	originalSkipVerify := common.TLSInsecureSkipVerify
	original := common.InsecureTLSConfig.ClientSessionCache
	common.TLSInsecureSkipVerify = true
	t.Cleanup(func() {
		common.TLSInsecureSkipVerify = originalSkipVerify
		common.InsecureTLSConfig.ClientSessionCache = original
	})

	conf := buildUpstreamTLSConfig()
	if conf == common.InsecureTLSConfig {
		t.Fatal("buildUpstreamTLSConfig returned global InsecureTLSConfig")
	}
	if conf.ClientSessionCache == nil {
		t.Fatal("local TLS config missing ClientSessionCache")
	}
	if common.InsecureTLSConfig.ClientSessionCache != original {
		t.Fatal("global InsecureTLSConfig was mutated")
	}
}

func TestBuildUpstreamTransportHTTP1Only(t *testing.T) {
	transport := buildUpstreamTransportHTTP1Only(http.ProxyFromEnvironment, nil)

	if transport.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 should be false for HTTP/1.1-only transport")
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil")
	}
	if len(transport.TLSClientConfig.NextProtos) != 1 || transport.TLSClientConfig.NextProtos[0] != "http/1.1" {
		t.Fatalf("NextProtos = %#v, want [\"http/1.1\"]", transport.TLSClientConfig.NextProtos)
	}
	if containsString(transport.TLSClientConfig.NextProtos, "h2") {
		t.Fatal("HTTP/1.1-only transport must not include h2 in NextProtos")
	}
	if transport.IdleConnTimeout != upstreamIdleConnTimeout {
		t.Fatalf("IdleConnTimeout = %s, want %s", transport.IdleConnTimeout, upstreamIdleConnTimeout)
	}
}

func TestHTTP1OnlyTransport_NegotiatesHTTP11(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	h1Transport := buildUpstreamTransportHTTP1Only(nil, nil)
	h1Transport.TLSClientConfig.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	client := &http.Client{Transport: h1Transport}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Proto != "HTTP/1.1" {
		t.Fatalf("expected HTTP/1.1, got %s", resp.Proto)
	}
}

func TestNewProxyHttpClientHTTP1Only_CacheConsistency(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	const proxyURL = "http://proxy.example:8080"

	c1, err := NewProxyHttpClientHTTP1Only(proxyURL)
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}
	c2, err := NewProxyHttpClientHTTP1Only(proxyURL)
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}
	if c1 != c2 {
		t.Fatal("same proxy URL should return same cached client")
	}

	h2Client, err := NewProxyHttpClient(proxyURL)
	if err != nil {
		t.Fatalf("H2 client returned error: %v", err)
	}
	if c1 == h2Client {
		t.Fatal("H1 and H2 proxy clients must be different instances")
	}
}
