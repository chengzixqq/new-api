package common

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	runtimepprof "runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/cpu"
)

const (
	defaultPprofAddress            = "127.0.0.1:8005"
	defaultPprofCPUThreshold       = 80
	defaultPprofSampleInterval     = 30 * time.Second
	defaultPprofProfileDuration    = 10 * time.Second
	defaultPprofProfileRetention   = 10
	defaultPprofProfileDirectory   = "./pprof"
	defaultPprofReadHeaderTimeout  = 5 * time.Second
	defaultPprofIdleTimeout        = 30 * time.Second
	defaultPprofMaxHeaderBytes     = 64 << 10
	maxPprofSampleIntervalSeconds  = 3600
	maxPprofProfileDurationSeconds = 300
	maxPprofProfileRetention       = 1000
)

type PprofConfig struct {
	Address          string
	CPUThreshold     float64
	SampleInterval   time.Duration
	ProfileDuration  time.Duration
	ProfileRetention int
	ProfileDirectory string
}

var cpuProfileActive atomic.Bool

func LoadPprofConfig() (PprofConfig, error) {
	config := PprofConfig{
		Address:          strings.TrimSpace(GetEnvOrDefaultString("PPROF_ADDR", defaultPprofAddress)),
		CPUThreshold:     float64(defaultPprofCPUThreshold),
		SampleInterval:   defaultPprofSampleInterval,
		ProfileDuration:  defaultPprofProfileDuration,
		ProfileRetention: defaultPprofProfileRetention,
		ProfileDirectory: strings.TrimSpace(GetEnvOrDefaultString("PPROF_DIR", defaultPprofProfileDirectory)),
	}

	if err := validatePprofAddress(config.Address); err != nil {
		return PprofConfig{}, err
	}
	if config.ProfileDirectory == "" {
		return PprofConfig{}, fmt.Errorf("PPROF_DIR must not be empty")
	}

	threshold, err := parsePprofFloat("PPROF_CPU_THRESHOLD_PERCENT", defaultPprofCPUThreshold, 0, 100)
	if err != nil {
		return PprofConfig{}, err
	}
	config.CPUThreshold = threshold

	sampleInterval, err := parsePprofInt("PPROF_CPU_SAMPLE_INTERVAL_SECONDS", int(defaultPprofSampleInterval/time.Second), 1, maxPprofSampleIntervalSeconds)
	if err != nil {
		return PprofConfig{}, err
	}
	config.SampleInterval = time.Duration(sampleInterval) * time.Second

	profileDuration, err := parsePprofInt("PPROF_CPU_PROFILE_DURATION_SECONDS", int(defaultPprofProfileDuration/time.Second), 1, maxPprofProfileDurationSeconds)
	if err != nil {
		return PprofConfig{}, err
	}
	config.ProfileDuration = time.Duration(profileDuration) * time.Second

	retention, err := parsePprofInt("PPROF_MAX_FILES", defaultPprofProfileRetention, 1, maxPprofProfileRetention)
	if err != nil {
		return PprofConfig{}, err
	}
	config.ProfileRetention = retention

	return config, nil
}

func NewPprofServer(config PprofConfig) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", httppprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)

	return &http.Server{
		Addr:              config.Address,
		Handler:           mux,
		ReadHeaderTimeout: defaultPprofReadHeaderTimeout,
		IdleTimeout:       defaultPprofIdleTimeout,
		MaxHeaderBytes:    defaultPprofMaxHeaderBytes,
	}
}

// Monitor periodically samples CPU usage and writes a bounded set of profiles.
// It returns promptly when ctx is canceled, including while a profile is active.
func Monitor(ctx context.Context, config PprofConfig) {
	monitorCPU(ctx, config, cpu.Percent, captureCPUProfile)
}

func monitorCPU(
	ctx context.Context,
	config PprofConfig,
	sample func(time.Duration, bool) ([]float64, error),
	capture func(context.Context, PprofConfig) error,
) {
	for {
		if ctx.Err() != nil {
			return
		}

		percent, err := sample(time.Second, false)
		if err != nil {
			SysError("sample CPU usage for pprof: " + err.Error())
		} else if len(percent) == 0 {
			SysError("sample CPU usage for pprof returned no values")
		} else if percent[0] > config.CPUThreshold && ctx.Err() == nil {
			SysLog(fmt.Sprintf("CPU usage %.2f%% exceeded pprof threshold %.2f%%", percent[0], config.CPUThreshold))
			if err := capture(ctx, config); err != nil {
				SysError("capture CPU profile: " + err.Error())
			}
		}

		timer := time.NewTimer(config.SampleInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func captureCPUProfile(ctx context.Context, config PprofConfig) error {
	if !cpuProfileActive.CompareAndSwap(false, true) {
		return fmt.Errorf("a CPU profile is already active")
	}
	defer cpuProfileActive.Store(false)

	if err := os.MkdirAll(config.ProfileDirectory, 0700); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(config.ProfileDirectory, 0700); err != nil {
			return fmt.Errorf("restrict profile directory permissions: %w", err)
		}
	}
	if err := removePartialProfiles(config.ProfileDirectory); err != nil {
		return err
	}

	baseName := fmt.Sprintf("cpu-%s.pprof", time.Now().Format("20060102-150405.000000000"))
	finalPath := filepath.Join(config.ProfileDirectory, baseName)
	partialPath := finalPath + ".tmp"
	file, err := os.OpenFile(partialPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create profile file: %w", err)
	}
	removePartial := true
	defer func() {
		_ = file.Close()
		if removePartial {
			_ = os.Remove(partialPath)
		}
	}()

	if runtime.GOOS != "windows" {
		if err := file.Chmod(0600); err != nil {
			return fmt.Errorf("restrict profile file permissions: %w", err)
		}
	}
	if err := runtimepprof.StartCPUProfile(file); err != nil {
		return fmt.Errorf("start CPU profile: %w", err)
	}

	timer := time.NewTimer(config.ProfileDuration)
	select {
	case <-ctx.Done():
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
	}
	runtimepprof.StopCPUProfile()

	if err := file.Close(); err != nil {
		return fmt.Errorf("close CPU profile: %w", err)
	}
	if err := os.Rename(partialPath, finalPath); err != nil {
		return fmt.Errorf("publish CPU profile: %w", err)
	}
	removePartial = false

	if err := pruneProfiles(config.ProfileDirectory, config.ProfileRetention); err != nil {
		return err
	}
	return nil
}

func removePartialProfiles(directory string) error {
	partialFiles, err := filepath.Glob(filepath.Join(directory, "cpu-*.pprof.tmp"))
	if err != nil {
		return fmt.Errorf("list partial CPU profiles: %w", err)
	}
	for _, path := range partialFiles {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove partial CPU profile %q: %w", path, err)
		}
	}
	return nil
}

func pruneProfiles(directory string, retention int) error {
	if retention < 1 {
		return fmt.Errorf("profile retention must be at least 1")
	}
	paths, err := filepath.Glob(filepath.Join(directory, "cpu-*.pprof"))
	if err != nil {
		return fmt.Errorf("list CPU profiles: %w", err)
	}
	type profileFile struct {
		path    string
		modTime time.Time
	}
	profiles := make([]profileFile, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect CPU profile %q: %w", path, err)
		}
		profiles = append(profiles, profileFile{path: path, modTime: info.ModTime()})
	}
	if len(profiles) <= retention {
		return nil
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].modTime.Equal(profiles[j].modTime) {
			return profiles[i].path > profiles[j].path
		}
		return profiles[i].modTime.After(profiles[j].modTime)
	})
	for _, profile := range profiles[retention:] {
		if err := os.Remove(profile.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove expired CPU profile %q: %w", profile.path, err)
		}
	}
	return nil
}

func validatePprofAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid PPROF_ADDR %q: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("PPROF_ADDR must use a numeric port from 1 to 65535, got %q", address)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("PPROF_ADDR must use a loopback address, got %q", address)
	}
	return nil
}

func parsePprofInt(name string, fallback, minimum, maximum int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be an integer from %d to %d", name, minimum, maximum)
	}
	return value, nil
}

func parsePprofFloat(name string, fallback, minimum, maximum float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= minimum || value > maximum {
		return 0, fmt.Errorf("%s must be greater than %.0f and at most %.0f", name, minimum, maximum)
	}
	return value, nil
}
