package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_health_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeModelHealthProbeBoundariesAndLatestFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	histories := []model.ModelHealthHistory{
		{ID: 1, Status: model.ModelHealthProbeSuccess, LatencyMs: 100, CheckedAt: now.Add(-10 * time.Minute).Unix()},
		{ID: 2, Status: model.ModelHealthProbeSuccess, LatencyMs: 200, CheckedAt: now.Add(-5 * time.Minute).Unix()},
		{ID: 3, Status: model.ModelHealthProbeSuccess, LatencyMs: 300, CheckedAt: now.Unix()},
	}

	aggregate := SummarizeModelHealthProbe(histories, 300, 0, now)
	assert.Equal(t, ModelHealthStatusNormal, aggregate.Status)
	assert.Equal(t, 100.0, aggregate.Availability)

	histories[2].Status = model.ModelHealthProbeFailure
	aggregate = SummarizeModelHealthProbe(histories, 300, 0, now)
	assert.Equal(t, ModelHealthStatusAbnormal, aggregate.Status)
}

func TestSummarizeModelHealthProbeIdleWhenInsufficientOrStale(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	insufficient := []model.ModelHealthHistory{
		{Status: model.ModelHealthProbeSuccess, CheckedAt: now.Unix()},
		{Status: model.ModelHealthProbeSuccess, CheckedAt: now.Unix()},
	}
	assert.Equal(t, ModelHealthStatusIdle, SummarizeModelHealthProbe(insufficient, 300, 0, now).Status)

	stale := []model.ModelHealthHistory{
		{Status: model.ModelHealthProbeSuccess, CheckedAt: now.Add(-20 * time.Minute).Unix()},
		{Status: model.ModelHealthProbeSuccess, CheckedAt: now.Add(-20 * time.Minute).Unix()},
		{Status: model.ModelHealthProbeSuccess, CheckedAt: now.Add(-20 * time.Minute).Unix()},
	}
	assert.Equal(t, ModelHealthStatusIdle, SummarizeModelHealthProbe(stale, 300, 0, now).Status)
}

func TestModelHealthPassiveStatusThresholds(t *testing.T) {
	assert.Equal(t, ModelHealthStatusIdle, ModelHealthPassiveStatus(29, 100))
	assert.Equal(t, ModelHealthStatusNormal, ModelHealthPassiveStatus(30, 99))
	assert.Equal(t, ModelHealthStatusFluctuating, ModelHealthPassiveStatus(30, 95))
	assert.Equal(t, ModelHealthStatusAbnormal, ModelHealthPassiveStatus(30, 94.99))
}

func TestSummarizeModelHealthProbeWeightsRunsAndUsesLatestBatchStatus(t *testing.T) {
	settingValue, ok := config.GlobalConfig.Get("model_health_setting").(*model_health_setting.ModelHealthSetting)
	require.True(t, ok)
	previousSetting := *settingValue
	settingValue.ActiveMinSamples = 3
	settingValue.HealthyThreshold = 80
	settingValue.FluctuatingThreshold = 60
	t.Cleanup(func() { *settingValue = previousSetting })
	now := time.Unix(1_700_000_000, 0)
	histories := []model.ModelHealthHistory{
		{ID: 1, TargetID: 1, ModelName: "model", ProbeRunID: "run-1", RunStartedAt: now.Add(-10 * time.Minute).Unix(), AttemptIndex: 1, SamplingMode: model.ModelHealthSamplingConfirmOnFailure, PlannedAttempts: 3, MinimumSuccesses: 2, Status: model.ModelHealthProbeSuccess, LatencyMs: 100, CheckedAt: now.Add(-10 * time.Minute).Unix()},
		{ID: 2, TargetID: 1, ModelName: "model", ProbeRunID: "run-2", RunStartedAt: now.Add(-5 * time.Minute).Unix(), AttemptIndex: 1, SamplingMode: model.ModelHealthSamplingConfirmOnFailure, PlannedAttempts: 3, MinimumSuccesses: 2, Status: model.ModelHealthProbeSuccess, LatencyMs: 100, CheckedAt: now.Add(-5 * time.Minute).Unix()},
		{ID: 3, TargetID: 1, ModelName: "model", ProbeRunID: "run-3", RunStartedAt: now.Unix(), AttemptIndex: 1, SamplingMode: model.ModelHealthSamplingFixed, PlannedAttempts: 3, MinimumSuccesses: 2, Status: model.ModelHealthProbeFailure, ErrorClass: ModelHealthErrorServer, LatencyMs: 100, CheckedAt: now.Unix()},
		{ID: 4, TargetID: 1, ModelName: "model", ProbeRunID: "run-3", RunStartedAt: now.Unix(), AttemptIndex: 2, SamplingMode: model.ModelHealthSamplingFixed, PlannedAttempts: 3, MinimumSuccesses: 2, Status: model.ModelHealthProbeSuccess, LatencyMs: 100, CheckedAt: now.Unix()},
		{ID: 5, TargetID: 1, ModelName: "model", ProbeRunID: "run-3", RunStartedAt: now.Unix(), AttemptIndex: 3, SamplingMode: model.ModelHealthSamplingFixed, PlannedAttempts: 3, MinimumSuccesses: 2, Status: model.ModelHealthProbeSuccess, LatencyMs: 100, CheckedAt: now.Unix()},
	}

	aggregate := SummarizeModelHealthProbe(histories, 300, 0, now)

	assert.EqualValues(t, 3, aggregate.RunCount)
	assert.EqualValues(t, 5, aggregate.AttemptCount)
	assert.EqualValues(t, 4, aggregate.SuccessCount)
	assert.Equal(t, 88.89, aggregate.Availability, "availability is the equal mean of 100, 100, and 66.67 rather than 4/5")
	assert.Equal(t, ModelHealthStatusFluctuating, aggregate.Status, "latest partial-success batch degrades an otherwise healthy history")
}
