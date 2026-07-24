package common

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPServerReadHeaderTimeout = 10 * time.Second
	defaultHTTPServerReadTimeout       = 300 * time.Second
	defaultHTTPServerIdleTimeout       = 120 * time.Second
	defaultHTTPServerMaxHeaderBytes    = 256 << 10

	minHTTPServerTimeoutSeconds = 1
	maxHTTPServerTimeoutSeconds = 3600
	minHTTPServerMaxHeaderBytes = 16 << 10
	maxHTTPServerMaxHeaderBytes = 1 << 20
)

// HTTPServerConfig contains the request-boundary settings for the public HTTP
// server. WriteTimeout intentionally remains zero so it cannot terminate SSE or
// other long-lived streaming responses.
type HTTPServerConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
}

// NewHTTPServer applies the validated public-server limits to an HTTP server.
func NewHTTPServer(address string, handler http.Handler, config HTTPServerConfig) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: config.ReadHeaderTimeout,
		ReadTimeout:       config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       config.IdleTimeout,
		MaxHeaderBytes:    config.MaxHeaderBytes,
	}
}

func LoadHTTPServerAddress(defaultPort int) (address string, port string, err error) {
	portNumber := defaultPort
	if rawPort := strings.TrimSpace(os.Getenv("PORT")); rawPort != "" {
		portNumber, err = strconv.Atoi(rawPort)
		if err != nil {
			return "", "", fmt.Errorf("PORT must be an integer: %w", err)
		}
	}
	if portNumber < 1 || portNumber > 65535 {
		return "", "", fmt.Errorf("PORT must be between 1 and 65535, got %d", portNumber)
	}
	port = strconv.Itoa(portNumber)
	return ":" + port, port, nil
}

// LoadHTTPServerConfig reads and strictly validates public server limits. A
// malformed or out-of-range value is an operator error and must stop startup;
// silently falling back would leave the process running with unexpected network
// exposure.
func LoadHTTPServerConfig() (HTTPServerConfig, error) {
	config := HTTPServerConfig{
		ReadHeaderTimeout: defaultHTTPServerReadHeaderTimeout,
		ReadTimeout:       defaultHTTPServerReadTimeout,
		WriteTimeout:      0,
		IdleTimeout:       defaultHTTPServerIdleTimeout,
		MaxHeaderBytes:    defaultHTTPServerMaxHeaderBytes,
	}

	readHeaderSeconds, err := parseBoundedHTTPServerEnv(
		"HTTP_SERVER_READ_HEADER_TIMEOUT_SECONDS",
		int(config.ReadHeaderTimeout/time.Second),
		minHTTPServerTimeoutSeconds,
		300,
	)
	if err != nil {
		return HTTPServerConfig{}, err
	}
	config.ReadHeaderTimeout = time.Duration(readHeaderSeconds) * time.Second

	readSeconds, err := parseBoundedHTTPServerEnv(
		"HTTP_SERVER_READ_TIMEOUT_SECONDS",
		int(config.ReadTimeout/time.Second),
		minHTTPServerTimeoutSeconds,
		maxHTTPServerTimeoutSeconds,
	)
	if err != nil {
		return HTTPServerConfig{}, err
	}
	config.ReadTimeout = time.Duration(readSeconds) * time.Second

	idleSeconds, err := parseBoundedHTTPServerEnv(
		"HTTP_SERVER_IDLE_TIMEOUT_SECONDS",
		int(config.IdleTimeout/time.Second),
		minHTTPServerTimeoutSeconds,
		maxHTTPServerTimeoutSeconds,
	)
	if err != nil {
		return HTTPServerConfig{}, err
	}
	config.IdleTimeout = time.Duration(idleSeconds) * time.Second

	maxHeaderBytes, err := parseBoundedHTTPServerEnv(
		"HTTP_SERVER_MAX_HEADER_BYTES",
		config.MaxHeaderBytes,
		minHTTPServerMaxHeaderBytes,
		maxHTTPServerMaxHeaderBytes,
	)
	if err != nil {
		return HTTPServerConfig{}, err
	}
	config.MaxHeaderBytes = maxHeaderBytes

	return config, nil
}

func parseBoundedHTTPServerEnv(name string, defaultValue, minimum, maximum int) (int, error) {
	rawValue, exists := os.LookupEnv(name)
	if !exists {
		return defaultValue, nil
	}
	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return 0, fmt.Errorf("%s must not be empty", name)
	}

	value, err := strconv.Atoi(rawValue)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d, got %d", name, minimum, maximum, value)
	}
	return value, nil
}
