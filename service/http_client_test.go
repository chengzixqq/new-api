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

	"github.com/QuantumNous/new-api/common"
)

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
