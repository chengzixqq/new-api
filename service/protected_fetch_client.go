package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

type ssrfResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type protectedFetchPinnedTarget struct {
	host string
	port string
	ips  []net.IP
}

type protectedFetchTargetContextKey struct{}

type protectedFetchDialer struct {
	resolver      ssrfResolver
	dialContext   func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection func() (*common.SSRFProtection, bool, error)
}

// ssrfProtectedRoundTripper resolves each request target exactly once, validates
// every returned address, and passes the validated addresses to the dialer via
// the request context. It intentionally never consults environment proxy
// settings: user-controlled downloads must connect directly to the verified IP.
type ssrfProtectedRoundTripper struct {
	resolver              ssrfResolver
	dialContext           func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection         func() (*common.SSRFProtection, bool, error)
	responseHeaderTimeout time.Duration
	tlsConfig             *tls.Config
}

func currentFetchProtection() (*common.SSRFProtection, bool, error) {
	fetchSetting := system_setting.GetFetchSetting()
	if !fetchSetting.EnableSSRFProtection {
		return nil, false, nil
	}

	protection, err := common.NewSSRFProtectionFromFetchSetting(
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	)
	if err != nil {
		return nil, true, err
	}
	return protection, true, nil
}

func newProtectedFetchHTTPClient() *http.Client {
	return newProtectedFetchHTTPClientWithDialer(nil, nil, nil)
}

// NewSSRFProtectedHTTPClient returns a direct-only client that resolves and
// validates every request and redirect target before dialing its pinned IP.
// Callers that need an independently scoped client can use this constructor;
// shared download paths should continue using GetSSRFProtectedHTTPClient.
func NewSSRFProtectedHTTPClient() *http.Client {
	return newProtectedFetchHTTPClient()
}

func newProtectedFetchHTTPClientWithDialer(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error)) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialContext == nil {
		netDialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		dialContext = netDialer.DialContext
	}
	if getProtection == nil {
		getProtection = currentFetchProtection
	}

	return &http.Client{
		Transport: &ssrfProtectedRoundTripper{
			resolver:              resolver,
			dialContext:           dialContext,
			getProtection:         getProtection,
			responseHeaderTimeout: mediaResponseHeaderTimeout,
		},
		CheckRedirect: checkProtectedFetchRedirect,
	}
}

func (t *ssrfProtectedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("invalid request")
	}

	target, err := resolveProtectedFetchTarget(req.Context(), req.URL, t.resolver, t.getProtection)
	if err != nil {
		return nil, err
	}

	requestContext := context.WithValue(req.Context(), protectedFetchTargetContextKey{}, target)
	request := req.Clone(requestContext)
	responseHeaderTimeout := t.responseHeaderTimeout
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = mediaResponseHeaderTimeout
	}
	tlsConfig := buildUpstreamTLSConfig()
	if t.tlsConfig != nil {
		tlsConfig = t.tlsConfig.Clone()
	}
	transport := &http.Transport{
		MaxIdleConns:          common.RelayMaxIdleConns,
		MaxIdleConnsPerHost:   common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:       time.Duration(common.RelayIdleConnTimeout) * time.Second,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true,
		Proxy:                 nil,
		DialContext:           (&protectedFetchDialer{dialContext: t.dialContext}).DialContext,
		ResponseHeaderTimeout: responseHeaderTimeout,
		TLSHandshakeTimeout:   upstreamTLSHandshakeTimeout,
		ExpectContinueTimeout: upstreamExpectContinueTimeout,
		TLSClientConfig:       tlsConfig,
	}
	return transport.RoundTrip(request)
}

func (t *ssrfProtectedRoundTripper) CloseIdleConnections() {}

func resolveProtectedFetchTarget(ctx context.Context, targetURL *url.URL, resolver ssrfResolver, getProtection func() (*common.SSRFProtection, bool, error)) (*protectedFetchPinnedTarget, error) {
	if targetURL == nil {
		return nil, fmt.Errorf("invalid URL")
	}
	if targetURL.Scheme != "http" && targetURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported protocol: %s (only http/https allowed)", targetURL.Scheme)
	}

	host := targetURL.Hostname()
	if host == "" {
		return nil, fmt.Errorf("invalid host")
	}
	portText := targetURL.Port()
	if portText == "" {
		if targetURL.Scheme == "https" {
			portText = "443"
		} else {
			portText = "80"
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %s", portText)
	}

	protection, enabled, err := getProtection()
	if err != nil {
		return nil, err
	}
	if enabled {
		if protection == nil {
			return nil, fmt.Errorf("SSRF protection is enabled without a policy")
		}
		if err := protection.ValidateNetworkTarget(host, port); err != nil {
			return nil, err
		}
	}

	if ip := net.ParseIP(host); ip != nil {
		return &protectedFetchPinnedTarget{host: host, port: portText, ips: []net.IP{ip}}, nil
	}

	resolved, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %s: %w", host, err)
	}

	candidateIPs := make([]net.IP, 0, len(resolved))
	for _, ipAddr := range resolved {
		ip := ipAddr.IP
		if ip == nil || ip.To16() == nil {
			continue
		}
		// Private, loopback, link-local, reserved, and configured IP ranges are
		// checked for every hostname. Skipping this check would allow a DNS name
		// to bypass the literal-IP policy and rebind into the local network.
		if enabled {
			if err := protection.ValidateResolvedIP(host, ip); err != nil {
				return nil, err
			}
		}
		candidateIPs = append(candidateIPs, append(net.IP(nil), ip...))
	}
	if len(candidateIPs) == 0 {
		return nil, fmt.Errorf("DNS resolution for %s returned no usable IP addresses", host)
	}

	return &protectedFetchPinnedTarget{host: host, port: portText, ips: candidateIPs}, nil
}

func (d *protectedFetchDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if target, ok := ctx.Value(protectedFetchTargetContextKey{}).(*protectedFetchPinnedTarget); ok && target != nil {
		host, portText, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid dial address %s: %w", addr, err)
		}
		if normalizeProtectedFetchHost(host) != normalizeProtectedFetchHost(target.host) || portText != target.port {
			return nil, fmt.Errorf("protected fetch target changed from %s to %s", net.JoinHostPort(target.host, target.port), addr)
		}
		return dialProtectedFetchIPs(ctx, d.dialContext, network, portText, target.ips)
	}

	if d.resolver == nil || d.getProtection == nil {
		return nil, fmt.Errorf("protected fetch dial missing pinned target")
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %s: %w", addr, err)
	}
	targetURL := &url.URL{Scheme: "http", Host: net.JoinHostPort(host, portText)}
	target, err := resolveProtectedFetchTarget(ctx, targetURL, d.resolver, d.getProtection)
	if err != nil {
		return nil, err
	}
	return dialProtectedFetchIPs(ctx, d.dialContext, network, portText, target.ips)
}

func dialProtectedFetchIPs(ctx context.Context, dialContext func(context.Context, string, string) (net.Conn, error), network, portText string, ips []net.IP) (net.Conn, error) {
	var lastDialErr error
	for _, ip := range ips {
		if !networkAllowsIP(network, ip) {
			continue
		}
		conn, err := dialContext(ctx, network, net.JoinHostPort(ip.String(), portText))
		if err == nil {
			return conn, nil
		}
		lastDialErr = err
	}
	if lastDialErr != nil {
		return nil, lastDialErr
	}
	return nil, fmt.Errorf("no usable IP addresses for network %s", network)
}

func normalizeProtectedFetchHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func networkAllowsIP(network string, ip net.IP) bool {
	switch network {
	case "tcp4":
		return ip.To4() != nil
	case "tcp6":
		return ip.To4() == nil
	default:
		return true
	}
}
