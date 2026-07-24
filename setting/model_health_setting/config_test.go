package model_health_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAliasRequiresExplicitNonEmptyMapping(t *testing.T) {
	original := modelHealthSetting
	t.Cleanup(func() { modelHealthSetting = original })
	modelHealthSetting.PublicModels = []PublicAlias{
		{Name: "internal-a", Alias: "Public A"},
		{Name: "internal-b", Alias: ""},
	}

	alias, ok := ResolvePublicModelAlias("internal-a")
	require.True(t, ok)
	assert.Equal(t, "Public A", alias)

	_, ok = ResolvePublicModelAlias("internal-b")
	assert.False(t, ok)
	_, ok = ResolvePublicModelAlias("not-listed")
	assert.False(t, ok)
}

func TestGetSettingNormalizesUnsafeValues(t *testing.T) {
	original := modelHealthSetting
	t.Cleanup(func() { modelHealthSetting = original })
	modelHealthSetting.DefaultIntervalSeconds = 1
	modelHealthSetting.DefaultTimeoutSeconds = 0
	modelHealthSetting.Concurrency = 1000
	modelHealthSetting.RetentionDays = 0
	modelHealthSetting.HealthyThreshold = 101
	modelHealthSetting.FluctuatingThreshold = -1
	modelHealthSetting.ActiveMinSamples = 0
	modelHealthSetting.PassiveMinSamples = 0
	modelHealthSetting.PoolDetailsEnabled = true
	modelHealthSetting.MultiSampleEnabled = true
	modelHealthSetting.DefaultSamplingMode = "invalid"
	modelHealthSetting.DefaultSamplesPerRun = 99
	modelHealthSetting.DefaultMinimumSuccesses = 99
	modelHealthSetting.DefaultSampleSpacingSeconds = 0

	setting := GetSetting()

	assert.Equal(t, DefaultIntervalSeconds, setting.DefaultIntervalSeconds)
	assert.Equal(t, DefaultTimeoutSeconds, setting.DefaultTimeoutSeconds)
	assert.Equal(t, 32, setting.Concurrency)
	assert.Equal(t, DefaultRetentionDays, setting.RetentionDays)
	assert.Equal(t, DefaultHealthyPercent, setting.HealthyThreshold)
	assert.Equal(t, DefaultWarningPercent, setting.FluctuatingThreshold)
	assert.EqualValues(t, DefaultActiveSamples, setting.ActiveMinSamples)
	assert.EqualValues(t, DefaultPassiveSamples, setting.PassiveMinSamples)
	assert.True(t, setting.PoolDetailsEnabled)
	assert.True(t, setting.MultiSampleEnabled)
	assert.Equal(t, DefaultSamplingMode, setting.DefaultSamplingMode)
	assert.Equal(t, DefaultSamplesPerRun, setting.DefaultSamplesPerRun)
	assert.Equal(t, DefaultMinimumSuccesses, setting.DefaultMinimumSuccesses)
	assert.Equal(t, DefaultSampleSpacingSeconds, setting.DefaultSampleSpacingSeconds)
}

func TestGetSettingLimitsSamplingDefaultsToScheduleBudget(t *testing.T) {
	original := modelHealthSetting
	t.Cleanup(func() { modelHealthSetting = original })
	modelHealthSetting.DefaultIntervalSeconds = 60
	modelHealthSetting.DefaultTimeoutSeconds = 45
	modelHealthSetting.DefaultSamplingMode = DefaultSamplingMode
	modelHealthSetting.DefaultSamplesPerRun = 3
	modelHealthSetting.DefaultMinimumSuccesses = 2
	modelHealthSetting.DefaultSampleSpacingSeconds = 3

	setting := GetSetting()

	assert.Equal(t, 1, setting.DefaultSamplesPerRun)
	assert.Equal(t, 1, setting.DefaultMinimumSuccesses)
}
