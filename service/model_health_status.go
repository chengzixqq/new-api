package service

import (
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/model_health_setting"
)

const (
	ModelHealthStatusNormal      = "healthy"
	ModelHealthStatusFluctuating = "fluctuating"
	ModelHealthStatusAbnormal    = "unstable"
	ModelHealthStatusIdle        = "idle"
)

type ModelHealthProbeAggregate struct {
	Status          string  `json:"status"`
	Availability    float64 `json:"availability"`
	SampleCount     int64   `json:"sample_count"`
	RunCount        int64   `json:"probe_run_count"`
	AttemptCount    int64   `json:"probe_attempt_count"`
	SuccessCount    int64   `json:"success_count"`
	LatestLatencyMs int64   `json:"latest_latency_ms"`
	AvgLatencyMs    int64   `json:"avg_latency_ms"`
	LatestCheckedAt int64   `json:"latest_checked_at"`
}

type ModelHealthProbeBatch struct {
	RunStartedAt    int64
	CheckedAt       int64
	Availability    float64
	Status          string
	AttemptCount    int64
	SuccessCount    int64
	LatestLatencyMs int64
	avgLatencyMs    int64
	sourceKey       string
}

func BuildModelHealthProbeBatches(histories []model.ModelHealthHistory) []ModelHealthProbeBatch {
	sortedHistories := append([]model.ModelHealthHistory(nil), histories...)
	sort.SliceStable(sortedHistories, func(i, j int) bool {
		if sortedHistories[i].CheckedAt == sortedHistories[j].CheckedAt {
			return sortedHistories[i].ID < sortedHistories[j].ID
		}
		return sortedHistories[i].CheckedAt < sortedHistories[j].CheckedAt
	})
	type batchBuilder struct {
		histories []model.ModelHealthHistory
		order     int
	}
	builders := make(map[string]*batchBuilder, len(sortedHistories))
	keys := make([]string, 0, len(sortedHistories))
	for index, history := range sortedHistories {
		key := strconv.FormatInt(history.TargetID, 10) + "\x00" + history.ModelName + "\x00" + history.ProbeRunID
		if history.ProbeRunID == "" {
			key += "\x00legacy\x00" + strconv.Itoa(index)
		}
		builder := builders[key]
		if builder == nil {
			builder = &batchBuilder{order: index}
			builders[key] = builder
			keys = append(keys, key)
		}
		builder.histories = append(builder.histories, history)
	}
	sort.SliceStable(keys, func(i, j int) bool { return builders[keys[i]].order < builders[keys[j]].order })
	batches := make([]ModelHealthProbeBatch, 0, len(keys))
	for _, key := range keys {
		attempts := builders[key].histories
		batch := ModelHealthProbeBatch{
			AttemptCount: int64(len(attempts)),
			sourceKey:    strconv.FormatInt(attempts[0].TargetID, 10) + "\x00" + attempts[0].ModelName,
		}
		minimumSuccesses := 1
		deterministic := false
		var latencySum, latencyCount int64
		for _, attempt := range attempts {
			if attempt.Status == model.ModelHealthProbeSuccess {
				batch.SuccessCount++
			}
			if attempt.LatencyMs >= 0 {
				latencySum += attempt.LatencyMs
				latencyCount++
			}
			if attempt.RunStartedAt > 0 && (batch.RunStartedAt == 0 || attempt.RunStartedAt < batch.RunStartedAt) {
				batch.RunStartedAt = attempt.RunStartedAt
			}
			if attempt.CheckedAt >= batch.CheckedAt {
				batch.CheckedAt = attempt.CheckedAt
				batch.LatestLatencyMs = attempt.LatencyMs
			}
			if attempt.MinimumSuccesses > 0 {
				minimumSuccesses = attempt.MinimumSuccesses
			}
			if isDeterministicModelHealthProbeResult(ModelHealthProbeResult{ErrorClass: attempt.ErrorClass}) {
				deterministic = true
			}
		}
		if batch.RunStartedAt == 0 {
			batch.RunStartedAt = batch.CheckedAt
		}
		if latencyCount > 0 {
			batch.avgLatencyMs = latencySum / latencyCount
		}
		if batch.AttemptCount > 0 {
			batch.Availability = math.Round(float64(batch.SuccessCount)/float64(batch.AttemptCount)*10000) / 100
		}
		switch {
		case batch.AttemptCount > 0 && batch.SuccessCount == batch.AttemptCount:
			batch.Status = ModelHealthStatusNormal
		case deterministic || batch.SuccessCount < int64(minimumSuccesses):
			batch.Status = ModelHealthStatusAbnormal
		default:
			batch.Status = ModelHealthStatusFluctuating
		}
		batches = append(batches, batch)
	}
	return batches
}

// SummarizeModelHealthProbe applies the current active-probe status contract.
// The status window is always the most recent 12 hours even when callers load
// a longer series window.
func SummarizeModelHealthProbe(histories []model.ModelHealthHistory, intervalSeconds int, latencySLOMs int64, now time.Time) ModelHealthProbeAggregate {
	setting := model_health_setting.GetSetting()
	windowStart := now.Add(-12 * time.Hour).Unix()
	allBatches := BuildModelHealthProbeBatches(histories)
	filtered := make([]ModelHealthProbeBatch, 0, len(allBatches))
	for _, batch := range allBatches {
		if batch.CheckedAt >= windowStart && batch.CheckedAt <= now.Unix() {
			filtered = append(filtered, batch)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].CheckedAt < filtered[j].CheckedAt })

	aggregate := ModelHealthProbeAggregate{Status: ModelHealthStatusIdle}
	var availabilitySum float64
	var latencySum int64
	for _, batch := range filtered {
		aggregate.RunCount++
		aggregate.AttemptCount += batch.AttemptCount
		aggregate.SuccessCount += batch.SuccessCount
		availabilitySum += batch.Availability
		latencySum += batch.avgLatencyMs
	}
	aggregate.SampleCount = aggregate.RunCount
	if aggregate.RunCount > 0 {
		latest := filtered[len(filtered)-1]
		aggregate.LatestLatencyMs = latest.LatestLatencyMs
		aggregate.LatestCheckedAt = latest.CheckedAt
		aggregate.Availability = math.Round(availabilitySum/float64(aggregate.RunCount)*100) / 100
		aggregate.AvgLatencyMs = latencySum / aggregate.RunCount
	}

	staleSeconds := int64(intervalSeconds * 3)
	if staleSeconds < int64((15 * time.Minute).Seconds()) {
		staleSeconds = int64((15 * time.Minute).Seconds())
	}
	if aggregate.RunCount < setting.ActiveMinSamples || aggregate.LatestCheckedAt == 0 || now.Unix()-aggregate.LatestCheckedAt > staleSeconds {
		return aggregate
	}
	historyStatus := ModelHealthStatusFromRate(aggregate.Availability, setting.HealthyThreshold, setting.FluctuatingThreshold)
	latestBySource := make(map[string]ModelHealthProbeBatch)
	for _, batch := range filtered {
		previous, exists := latestBySource[batch.sourceKey]
		if !exists || batch.CheckedAt >= previous.CheckedAt {
			latestBySource[batch.sourceKey] = batch
		}
	}
	statuses := make([]string, 0, len(latestBySource)+1)
	statuses = append(statuses, historyStatus)
	for _, batch := range latestBySource {
		statuses = append(statuses, batch.Status)
	}
	aggregate.Status = WorstModelHealthStatus(statuses)
	if latencySLOMs > 0 && aggregate.AvgLatencyMs > latencySLOMs {
		switch aggregate.Status {
		case ModelHealthStatusNormal:
			aggregate.Status = ModelHealthStatusFluctuating
		case ModelHealthStatusFluctuating:
			aggregate.Status = ModelHealthStatusAbnormal
		}
	}
	return aggregate
}

func ModelHealthPassiveStatus(requestCount int64, successRate float64) string {
	setting := model_health_setting.GetSetting()
	if requestCount < setting.PassiveMinSamples {
		return ModelHealthStatusIdle
	}
	return ModelHealthStatusFromRate(successRate, setting.HealthyThreshold, setting.FluctuatingThreshold)
}

func ModelHealthStatusFromRate(rate float64, healthyThreshold float64, fluctuatingThreshold float64) string {
	switch {
	case rate >= healthyThreshold:
		return ModelHealthStatusNormal
	case rate >= fluctuatingThreshold:
		return ModelHealthStatusFluctuating
	default:
		return ModelHealthStatusAbnormal
	}
}

func ModelHealthStatusesConflict(active, passive string) bool {
	return active != ModelHealthStatusIdle && passive != ModelHealthStatusIdle && active != passive
}

func WorstModelHealthStatus(statuses []string) string {
	worst := ModelHealthStatusIdle
	hasIdle := false
	for _, status := range statuses {
		if status == ModelHealthStatusIdle {
			hasIdle = true
			continue
		}
		if modelHealthStatusSeverity(status) > modelHealthStatusSeverity(worst) {
			worst = status
		}
	}
	if worst == ModelHealthStatusAbnormal || worst == ModelHealthStatusFluctuating {
		return worst
	}
	if hasIdle {
		return ModelHealthStatusIdle
	}
	return worst
}

func modelHealthStatusSeverity(status string) int {
	switch status {
	case ModelHealthStatusAbnormal:
		return 3
	case ModelHealthStatusFluctuating:
		return 2
	case ModelHealthStatusNormal:
		return 1
	default:
		return 0
	}
}
