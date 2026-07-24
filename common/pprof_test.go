package common

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPprofConfigRejectsNonLoopbackAddress(t *testing.T) {
	t.Setenv("PPROF_ADDR", "0.0.0.0:8005")

	_, err := LoadPprofConfig()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "loopback")
}

func TestLoadPprofConfigRejectsInvalidPortAndNonFiniteThreshold(t *testing.T) {
	t.Setenv("PPROF_ADDR", "127.0.0.1:70000")
	_, err := LoadPprofConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "numeric port")

	t.Setenv("PPROF_ADDR", "127.0.0.1:8005")
	t.Setenv("PPROF_CPU_THRESHOLD_PERCENT", "NaN")
	_, err = LoadPprofConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PPROF_CPU_THRESHOLD_PERCENT")
}

func TestLoadPprofConfigAcceptsLoopbackAndValidatesBounds(t *testing.T) {
	t.Setenv("PPROF_ADDR", "[::1]:9005")
	t.Setenv("PPROF_CPU_THRESHOLD_PERCENT", "75.5")
	t.Setenv("PPROF_CPU_SAMPLE_INTERVAL_SECONDS", "12")
	t.Setenv("PPROF_CPU_PROFILE_DURATION_SECONDS", "7")
	t.Setenv("PPROF_MAX_FILES", "9")
	t.Setenv("PPROF_DIR", "profiles")

	config, err := LoadPprofConfig()

	require.NoError(t, err)
	assert.Equal(t, "[::1]:9005", config.Address)
	assert.Equal(t, 75.5, config.CPUThreshold)
	assert.Equal(t, 12*time.Second, config.SampleInterval)
	assert.Equal(t, 7*time.Second, config.ProfileDuration)
	assert.Equal(t, 9, config.ProfileRetention)
	assert.Equal(t, "profiles", config.ProfileDirectory)

	t.Setenv("PPROF_MAX_FILES", "0")
	_, err = LoadPprofConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PPROF_MAX_FILES")
}

func TestNewPprofServerUsesDedicatedMuxAndLimits(t *testing.T) {
	server := NewPprofServer(PprofConfig{Address: "127.0.0.1:8005"})

	assert.Equal(t, defaultPprofReadHeaderTimeout, server.ReadHeaderTimeout)
	assert.Equal(t, defaultPprofIdleTimeout, server.IdleTimeout)
	assert.Equal(t, defaultPprofMaxHeaderBytes, server.MaxHeaderBytes)

	indexResponse := httptest.NewRecorder()
	server.Handler.ServeHTTP(indexResponse, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	assert.Equal(t, http.StatusOK, indexResponse.Code)

	rootResponse := httptest.NewRecorder()
	server.Handler.ServeHTTP(rootResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusNotFound, rootResponse.Code)
}

func TestMonitorCPUHandlesSamplerFailureAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	captureCalled := false
	config := PprofConfig{SampleInterval: time.Hour, CPUThreshold: 80}

	monitorCPU(
		ctx,
		config,
		func(time.Duration, bool) ([]float64, error) {
			cancel()
			return nil, errors.New("sampler unavailable")
		},
		func(context.Context, PprofConfig) error {
			captureCalled = true
			return nil
		},
	)

	assert.False(t, captureCalled)
}

func TestPruneProfilesRetainsNewestAndRemovesPartials(t *testing.T) {
	directory := t.TempDir()
	baseTime := time.Now().Add(-time.Hour)
	for index := 0; index < 4; index++ {
		path := filepath.Join(directory, "cpu-profile-"+string(rune('a'+index))+".pprof")
		require.NoError(t, os.WriteFile(path, []byte("profile"), 0600))
		modifiedAt := baseTime.Add(time.Duration(index) * time.Minute)
		require.NoError(t, os.Chtimes(path, modifiedAt, modifiedAt))
	}
	partialPath := filepath.Join(directory, "cpu-stale.pprof.tmp")
	require.NoError(t, os.WriteFile(partialPath, []byte("partial"), 0600))

	require.NoError(t, removePartialProfiles(directory))
	require.NoError(t, pruneProfiles(directory, 10))
	require.NoError(t, pruneProfiles(directory, 2))

	profiles, err := filepath.Glob(filepath.Join(directory, "cpu-*.pprof"))
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	assert.FileExists(t, filepath.Join(directory, "cpu-profile-c.pprof"))
	assert.FileExists(t, filepath.Join(directory, "cpu-profile-d.pprof"))
	assert.NoFileExists(t, partialPath)
}
