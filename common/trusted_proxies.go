package common

import (
	"fmt"
	"net"
	"os"
	"strings"
)

var defaultTrustedProxies = []string{
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
}

// GetTrustedProxies returns the proxy addresses whose forwarding headers Gin
// may trust. An explicit TRUSTED_PROXIES value replaces the deployment-friendly
// loopback/private-network defaults; use "none" to disable proxy trust.
func GetTrustedProxies() ([]string, error) {
	rawValue, configured := os.LookupEnv("TRUSTED_PROXIES")
	if !configured {
		return append([]string(nil), defaultTrustedProxies...), nil
	}

	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return nil, fmt.Errorf("TRUSTED_PROXIES must be a comma-separated list of IP addresses or CIDRs, or %q", "none")
	}
	if strings.EqualFold(rawValue, "none") {
		return nil, nil
	}

	entries := strings.Split(rawValue, ",")
	trustedProxies := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("TRUSTED_PROXIES contains an empty entry")
		}
		if strings.EqualFold(entry, "none") {
			return nil, fmt.Errorf("TRUSTED_PROXIES value %q cannot be combined with proxy addresses", "none")
		}
		if net.ParseIP(entry) == nil {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return nil, fmt.Errorf("invalid TRUSTED_PROXIES entry %q: expected an IP address or CIDR", entry)
			}
		}
		trustedProxies = append(trustedProxies, entry)
	}

	return trustedProxies, nil
}
