package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticSSRFResolver map[string][]net.IPAddr

func (r staticSSRFResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if ips, ok := r[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("unexpected lookup for %s", host)
}

type sequenceSSRFResolver struct {
	mutex     sync.Mutex
	responses [][]net.IPAddr
	lookups   int
}

func (r *sequenceSSRFResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.lookups >= len(r.responses) {
		return nil, fmt.Errorf("unexpected lookup %d for %s", r.lookups+1, host)
	}
	response := r.responses[r.lookups]
	r.lookups++
	return response, nil
}

func staticProtection(protection *common.SSRFProtection) func() (*common.SSRFProtection, bool, error) {
	return func() (*common.SSRFProtection, bool, error) {
		return protection, true, nil
	}
}

func testSSRFProtection() *common.SSRFProtection {
	return &common.SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		AllowedPorts:           []int{80, 443},
		ApplyIPFilterForDomain: true,
	}
}

func testConn(t *testing.T) net.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return clientConn
}

func TestProtectedFetchDialerRejectsPrivateReboundAddress(t *testing.T) {
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {{IP: net.ParseIP("127.0.0.1")}},
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			t.Fatalf("dialContext should not be called for blocked address %s", address)
			return nil, nil
		},
		getProtection: staticProtection(testSSRFProtection()),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:80")

	require.Error(t, err)
	require.Nil(t, conn)
	require.Contains(t, err.Error(), "private IP address not allowed")
}

func TestProtectedFetchDialerRejectsMixedResolvedIPs(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("10.0.0.1")},
				{IP: net.ParseIP("8.8.8.8")},
			},
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testConn(t), nil
		},
		getProtection: staticProtection(testSSRFProtection()),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:443")

	require.Error(t, err)
	require.Nil(t, conn)
	require.Empty(t, dialed)
	require.Contains(t, err.Error(), "private IP address not allowed")
}

func TestProtectedFetchDialerDialsPinnedAllowedIP(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("8.8.8.8")},
				{IP: net.ParseIP("1.1.1.1")},
			},
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testConn(t), nil
		},
		getProtection: staticProtection(testSSRFProtection()),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:443")

	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []string{"8.8.8.8:443"}, dialed)
}

func TestProtectedFetchDialerAllowsExplicitPrivateIPException(t *testing.T) {
	var dialed []string
	protection := testSSRFProtection()
	protection.AllowPrivateIp = true
	protection.IpFilterMode = true
	protection.IpList = []string{"10.0.0.0/8"}
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"internal.example": {{IP: net.ParseIP("10.1.2.3")}},
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testConn(t), nil
		},
		getProtection: staticProtection(protection),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "internal.example:80")

	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []string{"10.1.2.3:80"}, dialed)
}

func TestProtectedFetchAlwaysChecksResolvedPrivateIP(t *testing.T) {
	protection := testSSRFProtection()
	protection.ApplyIPFilterForDomain = false
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {{IP: net.ParseIP("169.254.169.254")}},
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			t.Fatalf("dialContext should not be called for blocked address %s", address)
			return nil, nil
		},
		getProtection: staticProtection(protection),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:80")

	require.Error(t, err)
	require.Nil(t, conn)
	require.Contains(t, err.Error(), "private IP address not allowed")
}

func TestGetSSRFProtectedHTTPClientStaysDirectWhenProtectionDisabled(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	originalProtectedClient := ssrfProtectedHTTPClient
	t.Cleanup(func() {
		*fetchSetting = originalFetchSetting
		ssrfProtectedHTTPClient = originalProtectedClient
	})

	fetchSetting.EnableSSRFProtection = false
	expected := &http.Client{}
	ssrfProtectedHTTPClient = expected

	require.Same(t, expected, GetSSRFProtectedHTTPClient())
}

func TestProtectedFetchRoundTripperIgnoresEnvironmentProxyAndDialsVerifiedIP(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:3128")
	var dialed []string
	client := newProtectedFetchHTTPClientWithDialer(
		staticSSRFResolver{"safe.example": {{IP: net.ParseIP("8.8.8.8")}}},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, errors.New("stop after verified direct dial")
		},
		staticProtection(testSSRFProtection()),
	)
	req, err := http.NewRequest(http.MethodGet, "http://safe.example/resource", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)

	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, []string{"8.8.8.8:80"}, dialed)
}

func TestProtectedFetchRoundTripperRejectsPrivateTargetBeforeDial(t *testing.T) {
	var dialed []string
	client := newProtectedFetchHTTPClientWithDialer(
		staticSSRFResolver{"safe.example": {{IP: net.ParseIP("127.0.0.1")}}},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, errors.New("private target should not be dialed")
		},
		staticProtection(testSSRFProtection()),
	)
	req, err := http.NewRequest(http.MethodGet, "http://safe.example/resource", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)

	require.Error(t, err)
	require.Nil(t, resp)
	require.Contains(t, err.Error(), "private IP address not allowed")
	require.Empty(t, dialed)
}

func TestProtectedFetchRedirectReResolvesAndBlocksDNSRebinding(t *testing.T) {
	var handledPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handledPaths = append(handledPaths, r.URL.Path)
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "http://safe.example/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("unexpected"))
	}))
	defer server.Close()

	resolver := &sequenceSSRFResolver{responses: [][]net.IPAddr{
		{{IP: net.ParseIP("8.8.8.8")}},
		{{IP: net.ParseIP("127.0.0.1")}},
	}}
	var dialed []string
	client := newProtectedFetchHTTPClientWithDialer(
		resolver,
		func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
		staticProtection(testSSRFProtection()),
	)
	req, err := http.NewRequest(http.MethodGet, "http://safe.example/start", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)

	require.Error(t, err)
	require.Nil(t, resp)
	require.Contains(t, err.Error(), "private IP address not allowed")
	assert.Equal(t, 2, resolver.lookups)
	assert.Equal(t, []string{"8.8.8.8:80"}, dialed)
	assert.Equal(t, []string{"/start"}, handledPaths)
}

func TestProtectedFetchResolutionInheritsRequestCancellation(t *testing.T) {
	resolver := blockingSSRFResolver{}
	client := newProtectedFetchHTTPClientWithDialer(
		resolver,
		func(ctx context.Context, network, address string) (net.Conn, error) {
			t.Fatal("dialContext must not run after a cancelled DNS lookup")
			return nil, nil
		},
		staticProtection(testSSRFProtection()),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://safe.example/resource", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, resp)
}

type blockingSSRFResolver struct{}

func (blockingSSRFResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestProtectedFetchRejectsNonHTTPRedirectTarget(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "file:///etc/passwd", nil)
	require.NoError(t, err)

	err = checkProtectedFetchRedirect(request, nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported protocol")
}
