package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

const (
	defaultUpstreamWarmupInterval    = 30 * time.Second
	defaultUpstreamWarmupTimeout     = 10 * time.Second
	minUpstreamWarmupInterval        = 5 * time.Second
	defaultUpstreamWarmupPath        = "/v1/models"
	defaultUpstreamWarmupUA          = "new-api-upstream-warmup/1.0"
	defaultUpstreamWarmupConcurrency = 8
	defaultUpstreamWarmupH1Conns     = 1
	minUpstreamWarmupConcurrency     = 1
	maxUpstreamWarmupConcurrency     = 32
)

var warmupForbiddenSubpaths = []string{
	"/chat/completions", "/responses", "/images/generations",
	"/embeddings", "/audio/", "/moderations", "/completions",
}

var (
	warmupProtoStore          sync.Map
	buildWarmupTargetsHook    = buildWarmupTargets
	upstreamWarmupEnabledHook = upstreamWarmupEnabled
)

type warmupTarget struct {
	key      string
	url      string
	host     string
	proxy    string
	client   *http.Client
	attempts int
}

func StartUpstreamWarmupTask() {
	interval := parseUpstreamWarmupDuration("UPSTREAM_WARMUP_INTERVAL", defaultUpstreamWarmupInterval)
	if interval < minUpstreamWarmupInterval {
		interval = minUpstreamWarmupInterval
	}
	timeout := parseUpstreamWarmupDuration("UPSTREAM_WARMUP_TIMEOUT", defaultUpstreamWarmupTimeout)
	jitter := parseUpstreamWarmupJitter("UPSTREAM_WARMUP_JITTER", 0.2)

	common.SysLog(fmt.Sprintf(
		"upstream warmup task started: enabled=%t interval=%s timeout=%s jitter=%.2f concurrency=%d h1_connections=%d user_agent=%q",
		upstreamWarmupEnabled(),
		interval,
		timeout,
		jitter,
		upstreamWarmupConcurrency(),
		upstreamWarmupH1Connections(),
		upstreamWarmupUserAgent(),
	))
	gopool.Go(func() {
		for {
			runUpstreamWarmupTick(timeout)
			time.Sleep(withJitter(interval, jitter))
		}
	})
}

// runUpstreamWarmupTick executes a single warmup round. Panic recovery is scoped
// to one tick so an occasional panic cannot permanently kill the warmup loop.
func runUpstreamWarmupTick(timeout time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("upstream warmup tick panic recovered: %v", r))
		}
	}()
	if !upstreamWarmupEnabledHook() {
		return
	}
	targets := buildWarmupTargetsHook()
	pruneWarmupStatus(targets)
	if len(targets) > 0 {
		warmTargets(targets, timeout)
	}
}

func buildWarmupTargets() []warmupTarget {
	seen := make(map[string]bool)
	var targets []warmupTarget

	globalHTTPConfig := model_setting.ResolveUpstreamHTTPConfig(dto.ChannelOtherSettings{})
	for _, raw := range parseUpstreamWarmupURLs(os.Getenv("UPSTREAM_WARMUP_URLS")) {
		targets = append(targets, makeResolvedWarmupTargets(raw, "", 0, globalHTTPConfig, seen)...)
	}

	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		common.SysError(fmt.Sprintf("upstream warmup: load channels failed: %v", err))
		return targets
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue
		}
		if !channel.GetOtherSettings().UpstreamWarmupEnabled {
			continue
		}

		base := channel.GetBaseURL()
		if base == "" && channel.Type >= 0 && channel.Type < len(constant.ChannelBaseURLs) {
			base = constant.ChannelBaseURLs[channel.Type]
		}
		if base == "" {
			continue
		}

		warmURL := joinWarmupPath(base, warmupPath())
		targets = append(targets, makeChannelWarmupTargets(channel, warmURL, seen)...)
	}
	return targets
}

func makeChannelWarmupTargets(channel *model.Channel, rawURL string, seen map[string]bool) []warmupTarget {
	if channel == nil {
		return nil
	}
	proxyURL := channel.GetSetting().Proxy
	resolved := model_setting.ResolveUpstreamHTTPConfig(channel.GetOtherSettings())
	return makeResolvedWarmupTargets(rawURL, proxyURL, channel.Id, resolved, seen)
}

func makeResolvedWarmupTargets(
	rawURL string,
	proxyURL string,
	channelID int,
	resolved model_setting.ResolvedUpstreamHTTPConfig,
	seen map[string]bool,
) []warmupTarget {
	u, ok := parseWarmupURL(rawURL)
	if !ok {
		return nil
	}
	rawURL = strings.TrimSpace(rawURL)
	proxyDigest := sha256.Sum256([]byte(proxyURL))
	proxyLabel := observableProxyLabel(proxyURL)
	baseKey := fmt.Sprintf("%s://%s|proxy=%x|channel=%d", u.Scheme, u.Host, proxyDigest, channelID)
	appendTarget := func(targets []warmupTarget, keySuffix string, client *http.Client, attempts int) []warmupTarget {
		key := baseKey + keySuffix
		if client == nil || seen[key] {
			return targets
		}
		seen[key] = true
		return append(targets, warmupTarget{
			key:      key,
			url:      rawURL,
			host:     u.Host,
			proxy:    proxyLabel,
			client:   client,
			attempts: attempts,
		})
	}

	targets := make([]warmupTarget, 0, resolved.HTTP2ConnectionPoolSize+1)
	if resolved.Mode == dto.UpstreamHTTPModeHTTP1 {
		client, err := getUpstreamHTTP1Client(proxyURL)
		if err != nil {
			common.SysError(fmt.Sprintf("upstream warmup: HTTP/1 client failed for channel %d (proxy=%q): %v", channelID, proxyLabel, err))
			return nil
		}
		return appendTarget(targets, "|http1", client, upstreamWarmupH1Connections())
	}

	for index := 0; index < resolved.HTTP2ConnectionPoolSize; index++ {
		client, selection, err := SelectUpstreamHTTPClient(UpstreamHTTPClientOptions{
			ChannelID:     channelID,
			ProxyURL:      proxyURL,
			Mode:          UpstreamHTTPModeAuto,
			HTTP2PoolSize: resolved.HTTP2ConnectionPoolSize,
			Method:        http.MethodGet,
		})
		if err != nil {
			common.SysError(fmt.Sprintf("upstream warmup: HTTP/2 pool client failed for channel %d (proxy=%q): %v", channelID, proxyLabel, err))
			return targets
		}
		keySuffix := fmt.Sprintf("|h2-shard=%d", selection.HTTP2Shard)
		attempts := 1
		if resolved.HTTP2ConnectionPoolSize == 1 {
			keySuffix = "|h2-shared"
			if resolved.Mode == dto.UpstreamHTTPModeAuto {
				attempts = 0
			}
		}
		targets = appendTarget(targets, keySuffix, client, attempts)
	}
	if resolved.Mode == dto.UpstreamHTTPModeHybrid {
		client, err := getUpstreamHTTP1Client(proxyURL)
		if err != nil {
			common.SysError(fmt.Sprintf("upstream warmup: hybrid HTTP/1 client failed for channel %d (proxy=%q): %v", channelID, proxyLabel, err))
			return targets
		}
		targets = appendTarget(targets, "|http1", client, 1)
	}
	return targets
}

func observableProxyLabel(proxyURL string) string {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return ""
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "configured"
	}
	return strings.ToLower(parsed.Scheme) + "://" + parsed.Host
}

func parseWarmupURL(rawURL string) (*url.URL, bool) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, false
	}

	lowerPath := strings.ToLower(u.Path)
	for _, bad := range warmupForbiddenSubpaths {
		if strings.Contains(lowerPath, bad) {
			common.SysError(fmt.Sprintf("upstream warmup: refuse billable path %q", rawURL))
			return nil, false
		}
	}
	return u, true
}

func warmTargets(targets []warmupTarget, timeout time.Duration) {
	concurrency := upstreamWarmupConcurrency()
	if concurrency > len(targets) {
		concurrency = len(targets)
	}
	if concurrency < minUpstreamWarmupConcurrency {
		concurrency = minUpstreamWarmupConcurrency
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, target := range targets {
		attempts := warmupAttemptCount(target)
		for i := 0; i < attempts; i++ {
			target := target
			wg.Add(1)
			sem <- struct{}{}
			gopool.Go(func() {
				defer wg.Done()
				defer func() {
					<-sem
				}()
				defer func() {
					if r := recover(); r != nil {
						recordWarmupFailure(target, 0, fmt.Errorf("warmup worker panic: %v", r))
					}
				}()
				warmOneTarget(target, timeout)
			})
		}
	}
	wg.Wait()
}

func warmupAttemptCount(target warmupTarget) int {
	if target.attempts > 0 {
		return target.attempts
	}
	if protoMajor, ok := cachedWarmupProto(target); ok && protoMajor == 1 {
		return upstreamWarmupH1Connections()
	}
	return 1
}

func warmOneTarget(target warmupTarget, timeout time.Duration) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
	if err != nil {
		recordWarmupFailure(target, 0, err)
		return
	}

	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", upstreamWarmupUserAgent())

	client := target.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		recordWarmupFailure(target, 0, err)
		return
	}
	recordWarmupProto(target, resp.ProtoMajor)

	bytesRead, drainErr := io.Copy(io.Discard, resp.Body)
	closeErr := resp.Body.Close()
	latency := time.Since(start)
	if drainErr != nil || closeErr != nil {
		recordWarmupDrainFailure(target, resp.StatusCode, latency, bytesRead, drainErr, closeErr)
		return
	}
	recordWarmupReusableSuccess(target, resp.StatusCode, latency)
}

func parseUpstreamWarmupURLs(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})

	urls := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			urls = append(urls, part)
		}
	}
	return urls
}

func parseUpstreamWarmupDuration(envName string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return fallback
	}

	if seconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(seconds) * time.Second
	}

	duration, err := time.ParseDuration(raw)
	if err != nil {
		common.SysError(fmt.Sprintf("invalid %s=%q, using %s", envName, raw, fallback))
		return fallback
	}
	return duration
}

func warmupPath() string {
	if path := strings.TrimSpace(os.Getenv("UPSTREAM_WARMUP_PATH")); path != "" {
		return path
	}
	return defaultUpstreamWarmupPath
}

func joinWarmupPath(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func withJitter(d time.Duration, frac float64) time.Duration {
	if frac <= 0 {
		return d
	}
	delta := float64(d) * frac
	return time.Duration(float64(d) - delta + rand.Float64()*2*delta)
}

func parseUpstreamWarmupJitter(envName string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || value > 0.5 {
		return fallback
	}
	return value
}

func upstreamWarmupConcurrency() int {
	return parseUpstreamWarmupConcurrency("UPSTREAM_WARMUP_CONCURRENCY", defaultUpstreamWarmupConcurrency)
}

func parseUpstreamWarmupConcurrency(envName string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		common.SysError(fmt.Sprintf("invalid %s=%q, using %d", envName, raw, fallback))
		return fallback
	}
	if value < minUpstreamWarmupConcurrency {
		return minUpstreamWarmupConcurrency
	}
	if value > maxUpstreamWarmupConcurrency {
		return maxUpstreamWarmupConcurrency
	}
	return value
}

func upstreamWarmupH1Connections() int {
	return parseUpstreamWarmupConcurrency("UPSTREAM_WARMUP_H1_CONNECTIONS", defaultUpstreamWarmupH1Conns)
}

func upstreamWarmupUserAgent() string {
	if ua := strings.TrimSpace(os.Getenv("UPSTREAM_WARMUP_UA")); ua != "" {
		return ua
	}
	return defaultUpstreamWarmupUA
}

func recordWarmupProto(target warmupTarget, protoMajor int) {
	if protoMajor > 0 {
		warmupProtoStore.Store(target.key, protoMajor)
	}
}

func cachedWarmupProto(target warmupTarget) (int, bool) {
	value, ok := warmupProtoStore.Load(target.key)
	if !ok {
		return 0, false
	}
	protoMajor, ok := value.(int)
	return protoMajor, ok
}

func upstreamWarmupEnabled() bool {
	return common.UpstreamWarmupEnabled.Load()
}
