package model_health_setting

import (
	"math"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

const (
	DefaultIntervalSeconds      = 300
	DefaultTimeoutSeconds       = 45
	DefaultConcurrency          = 4
	DefaultRetentionDays        = 30
	DefaultHealthyPercent       = 99.0
	DefaultWarningPercent       = 95.0
	DefaultPassiveSamples       = 30
	DefaultActiveSamples        = 3
	DefaultSamplingMode         = "confirm_on_failure"
	DefaultSamplesPerRun        = 3
	DefaultMinimumSuccesses     = 2
	DefaultSampleSpacingSeconds = 3
)

// PublicAlias is an explicit internal-name to public-name mapping. Public
// model-health responses must only use entries with both fields populated;
// there is deliberately no fallback to the internal name.
type PublicAlias struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
}

type ModelHealthSetting struct {
	Enabled                     bool          `json:"enabled"`
	PoolDetailsEnabled          bool          `json:"pool_details_enabled"`
	MultiSampleEnabled          bool          `json:"multi_sample_enabled"`
	DefaultIntervalSeconds      int           `json:"default_interval_seconds"`
	DefaultTimeoutSeconds       int           `json:"default_timeout_seconds"`
	DefaultSamplingMode         string        `json:"default_sampling_mode"`
	DefaultSamplesPerRun        int           `json:"default_samples_per_run"`
	DefaultMinimumSuccesses     int           `json:"default_minimum_successes"`
	DefaultSampleSpacingSeconds int           `json:"default_sample_spacing_seconds"`
	Concurrency                 int           `json:"concurrency"`
	RetentionDays               int           `json:"retention_days"`
	HealthyThreshold            float64       `json:"healthy_threshold"`
	FluctuatingThreshold        float64       `json:"fluctuating_threshold"`
	PassiveMinSamples           int64         `json:"passive_min_samples"`
	ActiveMinSamples            int64         `json:"active_min_samples"`
	PublicModels                []PublicAlias `json:"public_models"`
	PublicGroups                []PublicAlias `json:"public_groups"`
}

var modelHealthSetting = ModelHealthSetting{
	Enabled:                     false,
	PoolDetailsEnabled:          false,
	MultiSampleEnabled:          false,
	DefaultIntervalSeconds:      DefaultIntervalSeconds,
	DefaultTimeoutSeconds:       DefaultTimeoutSeconds,
	DefaultSamplingMode:         DefaultSamplingMode,
	DefaultSamplesPerRun:        DefaultSamplesPerRun,
	DefaultMinimumSuccesses:     DefaultMinimumSuccesses,
	DefaultSampleSpacingSeconds: DefaultSampleSpacingSeconds,
	Concurrency:                 DefaultConcurrency,
	RetentionDays:               DefaultRetentionDays,
	HealthyThreshold:            DefaultHealthyPercent,
	FluctuatingThreshold:        DefaultWarningPercent,
	PassiveMinSamples:           DefaultPassiveSamples,
	ActiveMinSamples:            DefaultActiveSamples,
	PublicModels:                []PublicAlias{},
	PublicGroups:                []PublicAlias{},
}

func init() {
	config.GlobalConfig.Register("model_health_setting", &modelHealthSetting)
}

// GetSetting returns a normalized copy. Persisted values are intentionally
// normalized at the read boundary so malformed legacy options cannot disable
// retention, remove sample guards, or create unbounded concurrency.
func GetSetting() ModelHealthSetting {
	setting := modelHealthSetting
	if setting.DefaultIntervalSeconds < 60 || setting.DefaultIntervalSeconds > 3600 {
		setting.DefaultIntervalSeconds = DefaultIntervalSeconds
	}
	if setting.DefaultTimeoutSeconds < 1 || setting.DefaultTimeoutSeconds > 300 {
		setting.DefaultTimeoutSeconds = DefaultTimeoutSeconds
	}
	if setting.DefaultTimeoutSeconds > setting.DefaultIntervalSeconds {
		setting.DefaultTimeoutSeconds = min(DefaultTimeoutSeconds, setting.DefaultIntervalSeconds)
	}
	setting.DefaultSamplingMode = strings.TrimSpace(setting.DefaultSamplingMode)
	if setting.DefaultSamplingMode != "fixed" && setting.DefaultSamplingMode != "confirm_on_failure" {
		setting.DefaultSamplingMode = DefaultSamplingMode
	}
	if setting.DefaultSamplesPerRun < 1 || setting.DefaultSamplesPerRun > 5 {
		setting.DefaultSamplesPerRun = DefaultSamplesPerRun
	}
	if setting.DefaultMinimumSuccesses < 1 || setting.DefaultMinimumSuccesses > setting.DefaultSamplesPerRun {
		setting.DefaultMinimumSuccesses = min(DefaultMinimumSuccesses, setting.DefaultSamplesPerRun)
	}
	if setting.DefaultSampleSpacingSeconds < 1 || setting.DefaultSampleSpacingSeconds > 30 {
		setting.DefaultSampleSpacingSeconds = DefaultSampleSpacingSeconds
	}
	maxSamples := (setting.DefaultIntervalSeconds + setting.DefaultSampleSpacingSeconds) /
		(setting.DefaultTimeoutSeconds + setting.DefaultSampleSpacingSeconds)
	if maxSamples < 1 {
		maxSamples = 1
	}
	if setting.DefaultSamplesPerRun > maxSamples {
		setting.DefaultSamplesPerRun = maxSamples
		if setting.DefaultMinimumSuccesses > maxSamples {
			setting.DefaultMinimumSuccesses = maxSamples
		}
	}
	if setting.Concurrency < 1 {
		setting.Concurrency = 1
	}
	if setting.Concurrency > 32 {
		setting.Concurrency = 32
	}
	if setting.RetentionDays < 1 {
		setting.RetentionDays = DefaultRetentionDays
	}
	if setting.RetentionDays > 365 {
		setting.RetentionDays = 365
	}
	if math.IsNaN(setting.HealthyThreshold) || math.IsInf(setting.HealthyThreshold, 0) || setting.HealthyThreshold <= 0 || setting.HealthyThreshold > 100 {
		setting.HealthyThreshold = DefaultHealthyPercent
	}
	if math.IsNaN(setting.FluctuatingThreshold) || math.IsInf(setting.FluctuatingThreshold, 0) || setting.FluctuatingThreshold <= 0 || setting.FluctuatingThreshold > setting.HealthyThreshold {
		setting.FluctuatingThreshold = setting.HealthyThreshold - (DefaultHealthyPercent - DefaultWarningPercent)
		if setting.FluctuatingThreshold <= 0 {
			setting.FluctuatingThreshold = setting.HealthyThreshold
		}
	}
	if setting.PassiveMinSamples < 1 {
		setting.PassiveMinSamples = DefaultPassiveSamples
	}
	if setting.ActiveMinSamples < 1 {
		setting.ActiveMinSamples = DefaultActiveSamples
	}
	setting.PublicModels = normalizePublicAliases(setting.PublicModels)
	setting.PublicGroups = normalizePublicAliases(setting.PublicGroups)
	return setting
}

func normalizePublicAliases(aliases []PublicAlias) []PublicAlias {
	const maxAliases = 200
	result := make([]PublicAlias, 0, min(len(aliases), maxAliases))
	seenNames := map[string]struct{}{}
	seenAliases := map[string]struct{}{}
	for _, item := range aliases {
		name, alias := strings.TrimSpace(item.Name), strings.TrimSpace(item.Alias)
		if name == "" || alias == "" || len(name) > 128 || len(alias) > 128 {
			continue
		}
		if _, exists := seenNames[name]; exists {
			continue
		}
		if _, exists := seenAliases[alias]; exists {
			continue
		}
		seenNames[name] = struct{}{}
		seenAliases[alias] = struct{}{}
		result = append(result, PublicAlias{Name: name, Alias: alias})
		if len(result) == maxAliases {
			break
		}
	}
	return result
}

func ResolvePublicModelAlias(name string) (string, bool) {
	return resolveAlias(GetSetting().PublicModels, name)
}

func ResolvePublicGroupAlias(name string) (string, bool) {
	return resolveAlias(GetSetting().PublicGroups, name)
}

func resolveAlias(aliases []PublicAlias, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	for _, item := range aliases {
		if strings.TrimSpace(item.Name) != name {
			continue
		}
		alias := strings.TrimSpace(item.Alias)
		if alias == "" {
			return "", false
		}
		return alias, true
	}
	return "", false
}
