package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLogRollupConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("LOG_ROLLUP_ENABLED", "")
	t.Setenv("LOG_ROLLUP_READ_ENABLED", "")
	t.Setenv("LOG_ROLLUP_REFRESH_SECONDS", "")
	t.Setenv("LOG_ROLLUP_BATCH_SIZE", "")
	config := GetLogRollupConfig()
	assert.False(t, config.Enabled)
	assert.False(t, config.ReadEnabled)
	assert.Equal(t, 5*time.Second, config.RefreshInterval)
	assert.Equal(t, 5000, config.BatchSize)
	assert.Equal(t, time.Minute, config.LeaseDuration)

	t.Setenv("LOG_ROLLUP_ENABLED", "true")
	t.Setenv("LOG_ROLLUP_READ_ENABLED", "true")
	t.Setenv("LOG_ROLLUP_REFRESH_SECONDS", "30")
	t.Setenv("LOG_ROLLUP_BATCH_SIZE", "123")
	config = GetLogRollupConfig()
	assert.True(t, config.Enabled)
	assert.True(t, config.ReadEnabled)
	assert.Equal(t, 30*time.Second, config.RefreshInterval)
	assert.Equal(t, 123, config.BatchSize)
	assert.Equal(t, 90*time.Second, config.LeaseDuration)
}

func TestStartLogRollupWorkerBackfillsWithoutRefreshDelay(t *testing.T) {
	t.Setenv("LOG_ROLLUP_ENABLED", "true")
	t.Setenv("LOG_ROLLUP_READ_ENABLED", "false")
	t.Setenv("LOG_ROLLUP_REFRESH_SECONDS", "30")
	t.Setenv("LOG_ROLLUP_BATCH_SIZE", "1")
	require.NoError(t, model.MigrateLogRollups(context.Background()))
	require.NoError(t, model.DB.Exec("DELETE FROM log_minute_rollups").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM log_rollup_states").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM logs").Error)
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM log_minute_rollups")
		model.DB.Exec("DELETE FROM log_rollup_states")
		model.DB.Exec("DELETE FROM logs")
	})

	base := time.Now().Add(-time.Minute).Unix()
	for i := 0; i < 3; i++ {
		require.NoError(t, model.DB.Create(&model.Log{
			CreatedAt: base + int64(i),
			Type:      model.LogTypeConsume,
			Quota:     10,
		}).Error)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := StartLogRollupWorker(ctx)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	t.Cleanup(func() { _ = worker.Stop(stopCtx) })

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var state model.LogRollupState
		err := model.DB.Where("state_key = ?", "log-minute-rollup-v1").First(&state).Error
		if err == nil && state.BackfillComplete && state.CaughtUp {
			assert.Equal(t, int64(3), state.WatermarkID)
			require.NoError(t, worker.Stop(stopCtx))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Fail(t, "worker did not finish three one-row batches before the 30-second refresh interval")
}

func TestDisabledLogRollupWorkerStopsImmediately(t *testing.T) {
	t.Setenv("LOG_ROLLUP_ENABLED", "false")
	worker := StartLogRollupWorker(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, worker.Stop(ctx))
}
