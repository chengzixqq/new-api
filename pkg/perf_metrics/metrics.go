package perfmetrics

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/go-redis/redis/v8"
)

var hotBuckets sync.Map
var hotChannelBuckets sync.Map

// seriesSchema is a stable client cache/schema marker. Do not change it when
// hiding fields or making response-only privacy hardening changes.
const seriesSchema = "dbcd0a3c01b55203"

func Init() {
	go flushLoop()
}

func RecordRelaySample(info *relaycommon.RelayInfo, success bool, outputTokens int64) {
	if info == nil || info.IsHealthProbe {
		return
	}
	now := time.Now()
	hasTtft := info.IsStream && info.HasSendResponse()
	ttftMs := int64(0)
	if hasTtft {
		ttftMs = info.FirstResponseTime.Sub(info.StartTime).Milliseconds()
	}
	latencyMs := now.Sub(info.StartTime).Milliseconds()
	generationMs := latencyMs
	if hasTtft {
		generationMs = now.Sub(info.FirstResponseTime).Milliseconds()
	}
	if generationMs <= 0 {
		generationMs = latencyMs
	}
	Record(Sample{
		Model:        info.OriginModelName,
		Group:        info.UsingGroup,
		LatencyMs:    latencyMs,
		TtftMs:       ttftMs,
		HasTtft:      hasTtft,
		Success:      success,
		OutputTokens: outputTokens,
		GenerationMs: generationMs,
	})
	if success {
		if attempt, ok := info.TakeChannelAttempt(); ok {
			RecordChannelAttempt(attempt, true, outputTokens)
		}
	}
}

// RecordChannelAttempt records one actual upstream dispatch. Failed retries
// pass an already-consumed snapshot so the next retry cannot overwrite its
// channel, group, or timing dimensions.
func RecordChannelAttempt(attempt relaycommon.ChannelAttempt, success bool, outputTokens int64) {
	if attempt.ChannelID <= 0 || attempt.ModelName == "" || attempt.IsHealthProbe || attempt.StartedAt.IsZero() {
		return
	}
	now := attempt.CompletedAt
	if now.IsZero() || now.Before(attempt.StartedAt) {
		now = time.Now()
	}
	hasTtft := attempt.IsStream && attempt.FirstResponseAt.After(attempt.StartedAt)
	ttftMs := int64(0)
	if hasTtft {
		ttftMs = attempt.FirstResponseAt.Sub(attempt.StartedAt).Milliseconds()
	}
	latencyMs := now.Sub(attempt.StartedAt).Milliseconds()
	generationMs := latencyMs
	if hasTtft {
		generationMs = now.Sub(attempt.FirstResponseAt).Milliseconds()
	}
	if generationMs <= 0 {
		generationMs = latencyMs
	}
	RecordChannelSample(attempt.ChannelID, Sample{
		Model:        attempt.ModelName,
		Group:        attempt.Group,
		LatencyMs:    latencyMs,
		TtftMs:       ttftMs,
		HasTtft:      hasTtft,
		Success:      success,
		OutputTokens: outputTokens,
		GenerationMs: generationMs,
	})
}

// RecordChannelSample adds counters for one real channel attempt. Channel
// metrics use their own hot/Redis buckets and never change legacy request-level
// perf_metrics semantics.
func RecordChannelSample(channelID int, sample Sample) {
	setting := perf_metrics_setting.GetSetting()
	if !setting.Enabled || channelID <= 0 || sample.Model == "" {
		return
	}
	if sample.Group == "" {
		sample.Group = "default"
	}
	if sample.LatencyMs < 0 {
		sample.LatencyMs = 0
	}
	key := channelBucketKey{
		channelID: channelID,
		model:     sample.Model,
		group:     sample.Group,
		bucketTs:  bucketStart(time.Now().Unix()),
	}
	actual, _ := hotChannelBuckets.LoadOrStore(key, &atomicBucket{})
	actual.(*atomicBucket).add(sample)
	recordChannelRedis(key, sample)
}

func Record(sample Sample) {
	setting := perf_metrics_setting.GetSetting()
	if !setting.Enabled || sample.Model == "" {
		return
	}
	if sample.Group == "" {
		sample.Group = "default"
	}
	if sample.LatencyMs < 0 {
		sample.LatencyMs = 0
	}

	key := bucketKey{
		model:    sample.Model,
		group:    sample.Group,
		bucketTs: bucketStart(time.Now().Unix()),
	}
	actual, _ := hotBuckets.LoadOrStore(key, &atomicBucket{})
	actual.(*atomicBucket).add(sample)
	recordRedis(key, sample)
}

func Query(params QueryParams) (QueryResult, error) {
	if params.Hours <= 0 {
		params.Hours = 24
	}
	if params.Hours > 24*30 {
		params.Hours = 24 * 30
	}
	endTs := time.Now().Unix()
	startTs := endTs - int64(params.Hours)*3600

	merged := map[bucketKey]counters{}
	rows, err := model.GetPerfMetrics(params.Model, params.Group, startTs, endTs)
	if err != nil {
		return QueryResult{}, err
	}
	for _, row := range rows {
		mergeCounters(merged, bucketKey{
			model:    row.ModelName,
			group:    row.Group,
			bucketTs: row.BucketTs,
		}, counters{
			requestCount:   row.RequestCount,
			successCount:   row.SuccessCount,
			totalLatencyMs: row.TotalLatencyMs,
			ttftSumMs:      row.TtftSumMs,
			ttftCount:      row.TtftCount,
			outputTokens:   row.OutputTokens,
			generationMs:   row.GenerationMs,
		})
	}

	if common.RedisEnabled && common.RDB != nil {
		mergeRedisQueryActive(merged, params, startTs, endTs)
	} else {
		hotBuckets.Range(func(key, value any) bool {
			k := key.(bucketKey)
			if k.model != params.Model || k.bucketTs < startTs || k.bucketTs > endTs {
				return true
			}
			if params.Group != "" && k.group != params.Group {
				return true
			}
			mergeCounters(merged, k, value.(*atomicBucket).snapshot())
			return true
		})
	}

	return buildQueryResult(params.Model, merged), nil
}

// QueryMatrix returns request-weighted passive metrics for arbitrary model and
// group selections. In Redis deployments the current bucket is read from the
// shared counters; without Redis only completed DB buckets are returned so a
// public page never presents one node's partial view as site-wide data.
func QueryMatrix(params MatrixParams) (MatrixResult, error) {
	if params.Hours <= 0 {
		params.Hours = 12
	}
	if params.Hours > 24*30 {
		params.Hours = 24 * 30
	}
	if params.BucketSeconds <= 0 {
		params.BucketSeconds = 300
	}
	now := time.Now().Unix()
	startTs := now - int64(params.Hours)*3600
	endTs := now
	currentBucket := bucketStart(now)
	if !common.RedisEnabled || common.RDB == nil {
		endTs = currentBucket - 1
	}

	rows, err := model.GetPerfMetricsMatrix(startTs, endTs, params.Models, params.Groups)
	if err != nil {
		return MatrixResult{}, err
	}

	merged := map[bucketKey]counters{}
	for _, row := range rows {
		ts := downsampleBucket(row.BucketTs, params.BucketSeconds)
		mergeCounters(merged, bucketKey{model: row.ModelName, group: row.Group, bucketTs: ts}, counters{
			requestCount: row.RequestCount, successCount: row.SuccessCount,
			totalLatencyMs: row.TotalLatencyMs, ttftSumMs: row.TtftSumMs,
			ttftCount: row.TtftCount, outputTokens: row.OutputTokens,
			generationMs: row.GenerationMs,
		})
	}
	mergeRedisMatrixCurrent(merged, params, currentBucket)

	return MatrixResult{
		StartTs: startTs, EndTs: now, BucketSeconds: params.BucketSeconds,
		Cells: buildMatrixCells(merged),
	}, nil
}

// QueryChannelMatrix returns request-weighted metrics for actual upstream
// attempts. Redis deployments merge the shared current bucket; deployments
// without Redis expose only completed DB buckets to avoid node-local data.
func QueryChannelMatrix(params ChannelMatrixParams) (MatrixResult, error) {
	if params.Hours <= 0 {
		params.Hours = 12
	}
	if params.Hours > 24*30 {
		params.Hours = 24 * 30
	}
	if params.BucketSeconds <= 0 {
		params.BucketSeconds = 300
	}
	now := time.Now().Unix()
	startTs := now - int64(params.Hours)*3600
	endTs := now
	currentBucket := bucketStart(now)
	if !common.RedisEnabled || common.RDB == nil {
		endTs = currentBucket - 1
	}

	rows, err := model.GetPerfChannelMetricsMatrix(startTs, endTs, params.ChannelIDs, params.Models, params.Groups)
	if err != nil {
		return MatrixResult{}, err
	}

	merged := map[channelBucketKey]counters{}
	for _, row := range rows {
		key := channelBucketKey{
			channelID: row.ChannelId,
			model:     row.ModelName,
			group:     row.Group,
			bucketTs:  downsampleBucket(row.BucketTs, params.BucketSeconds),
		}
		mergeChannelCounters(merged, key, counters{
			requestCount: row.RequestCount, successCount: row.SuccessCount,
			totalLatencyMs: row.TotalLatencyMs, ttftSumMs: row.TtftSumMs,
			ttftCount: row.TtftCount, outputTokens: row.OutputTokens,
			generationMs: row.GenerationMs,
		})
	}
	mergeRedisChannelMatrixCurrent(merged, params, currentBucket)

	return MatrixResult{
		StartTs: startTs, EndTs: now, BucketSeconds: params.BucketSeconds,
		Cells: buildChannelMatrixCells(merged),
	}, nil
}

func QuerySummaryAll(hours int, groups []string) (SummaryAllResult, error) {
	if hours <= 0 {
		hours = 24
	}
	if hours > 24*30 {
		hours = 24 * 30
	}
	endTs := time.Now().Unix()
	startTs := endTs - int64(hours)*3600
	allowedGroups := allowedGroupSet(groups)

	rows, err := model.GetPerfMetricsSummaryBucketsAll(startTs, endTs, groups)
	if err != nil {
		return SummaryAllResult{}, err
	}

	totals := map[string]counters{}
	modelBuckets := map[string]map[int64]counters{}
	for _, row := range rows {
		value := counters{
			requestCount:   row.RequestCount,
			successCount:   row.SuccessCount,
			totalLatencyMs: row.TotalLatencyMs,
			outputTokens:   row.OutputTokens,
			generationMs:   row.GenerationMs,
		}
		mergeModelTotals(totals, row.ModelName, value)
		mergeModelBucket(modelBuckets, row.ModelName, row.BucketTs, value)
	}

	if common.RedisEnabled && common.RDB != nil {
		mergeRedisSummaryActive(totals, modelBuckets, allowedGroups, startTs, endTs)
	} else {
		hotBuckets.Range(func(key, value any) bool {
			k := key.(bucketKey)
			if k.bucketTs < startTs || k.bucketTs > endTs {
				return true
			}
			if allowedGroups != nil {
				if _, ok := allowedGroups[k.group]; !ok {
					return true
				}
			}
			snap := value.(*atomicBucket).snapshot()
			if snap.requestCount == 0 {
				return true
			}
			mergeModelTotals(totals, k.model, snap)
			mergeModelBucket(modelBuckets, k.model, k.bucketTs, snap)
			return true
		})
	}

	models := make([]ModelSummary, 0, len(totals))
	for name, total := range totals {
		if total.requestCount == 0 {
			continue
		}
		avgLatency := total.totalLatencyMs / total.requestCount
		successRate := float64(total.successCount) / float64(total.requestCount) * 100
		avgTps := 0.0
		if total.generationMs > 0 {
			avgTps = float64(total.outputTokens) / (float64(total.generationMs) / 1000.0)
		}
		models = append(models, ModelSummary{
			ModelName:          name,
			AvgLatencyMs:       avgLatency,
			SuccessRate:        math.Round(successRate*100) / 100,
			AvgTps:             math.Round(avgTps*100) / 100,
			RecentSuccessRates: recentSuccessRates(modelBuckets[name], 3),
			RequestCount:       total.requestCount,
		})
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].RequestCount > models[j].RequestCount
	})

	return SummaryAllResult{Models: models}, nil
}

func mergeModelTotals(totals map[string]counters, modelName string, value counters) {
	if value.requestCount == 0 {
		return
	}
	current := totals[modelName]
	current.requestCount += value.requestCount
	current.successCount += value.successCount
	current.totalLatencyMs += value.totalLatencyMs
	current.ttftSumMs += value.ttftSumMs
	current.ttftCount += value.ttftCount
	current.outputTokens += value.outputTokens
	current.generationMs += value.generationMs
	totals[modelName] = current
}

func mergeModelBucket(modelBuckets map[string]map[int64]counters, modelName string, bucketTs int64, value counters) {
	if value.requestCount == 0 {
		return
	}
	if _, ok := modelBuckets[modelName]; !ok {
		modelBuckets[modelName] = map[int64]counters{}
	}
	current := modelBuckets[modelName][bucketTs]
	current.requestCount += value.requestCount
	current.successCount += value.successCount
	current.totalLatencyMs += value.totalLatencyMs
	current.ttftSumMs += value.ttftSumMs
	current.ttftCount += value.ttftCount
	current.outputTokens += value.outputTokens
	current.generationMs += value.generationMs
	modelBuckets[modelName][bucketTs] = current
}

func recentSuccessRates(buckets map[int64]counters, limit int) []float64 {
	if len(buckets) == 0 || limit <= 0 {
		return nil
	}
	timestamps := make([]int64, 0, len(buckets))
	for ts := range buckets {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i] < timestamps[j]
	})
	if len(timestamps) > limit {
		timestamps = timestamps[len(timestamps)-limit:]
	}
	rates := make([]float64, 0, len(timestamps))
	for _, ts := range timestamps {
		rates = append(rates, math.Round(successRate(buckets[ts])*100)/100)
	}
	return rates
}

func allowedGroupSet(groups []string) map[string]struct{} {
	if groups == nil {
		return nil
	}
	allowed := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		allowed[group] = struct{}{}
	}
	return allowed
}

func allowedChannelSet(channelIDs []int) map[int]struct{} {
	if channelIDs == nil {
		return nil
	}
	allowed := make(map[int]struct{}, len(channelIDs))
	for _, channelID := range channelIDs {
		if channelID > 0 {
			allowed[channelID] = struct{}{}
		}
	}
	return allowed
}

func bucketStart(ts int64) int64 {
	bucketSeconds := perf_metrics_setting.GetBucketSeconds()
	if bucketSeconds <= 0 {
		bucketSeconds = 3600
	}
	return ts - (ts % bucketSeconds)
}

func mergeCounters(merged map[bucketKey]counters, key bucketKey, value counters) {
	if value.requestCount == 0 {
		return
	}
	current := merged[key]
	current.requestCount += value.requestCount
	current.successCount += value.successCount
	current.totalLatencyMs += value.totalLatencyMs
	current.ttftSumMs += value.ttftSumMs
	current.ttftCount += value.ttftCount
	current.outputTokens += value.outputTokens
	current.generationMs += value.generationMs
	merged[key] = current
}

func mergeChannelCounters(merged map[channelBucketKey]counters, key channelBucketKey, value counters) {
	if value.requestCount == 0 {
		return
	}
	current := merged[key]
	current.requestCount += value.requestCount
	current.successCount += value.successCount
	current.totalLatencyMs += value.totalLatencyMs
	current.ttftSumMs += value.ttftSumMs
	current.ttftCount += value.ttftCount
	current.outputTokens += value.outputTokens
	current.generationMs += value.generationMs
	merged[key] = current
}

func buildQueryResult(modelName string, merged map[bucketKey]counters) QueryResult {
	groupBuckets := map[string]map[int64]counters{}
	for key, value := range merged {
		if value.requestCount == 0 {
			continue
		}
		if _, ok := groupBuckets[key.group]; !ok {
			groupBuckets[key.group] = map[int64]counters{}
		}
		groupBuckets[key.group][key.bucketTs] = value
	}

	groups := make([]string, 0, len(groupBuckets))
	for group := range groupBuckets {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	results := make([]GroupResult, 0, len(groups))
	for _, group := range groups {
		buckets := groupBuckets[group]
		timestamps := make([]int64, 0, len(buckets))
		for ts := range buckets {
			timestamps = append(timestamps, ts)
		}
		sort.Slice(timestamps, func(i, j int) bool {
			return timestamps[i] < timestamps[j]
		})

		total := counters{}
		series := make([]BucketPoint, 0, len(timestamps))
		for _, ts := range timestamps {
			value := buckets[ts]
			total.requestCount += value.requestCount
			total.successCount += value.successCount
			total.totalLatencyMs += value.totalLatencyMs
			total.ttftSumMs += value.ttftSumMs
			total.ttftCount += value.ttftCount
			total.outputTokens += value.outputTokens
			total.generationMs += value.generationMs
			series = append(series, bucketPoint(ts, value))
		}

		results = append(results, GroupResult{
			Group:          group,
			RequestCount:   total.requestCount,
			SuccessCount:   total.successCount,
			TotalLatencyMs: total.totalLatencyMs,
			TtftSumMs:      total.ttftSumMs,
			TtftCount:      total.ttftCount,
			OutputTokens:   total.outputTokens,
			GenerationMs:   total.generationMs,
			AvgTtftMs:      avg(total.ttftSumMs, total.ttftCount),
			AvgLatencyMs:   avg(total.totalLatencyMs, total.requestCount),
			SuccessRate:    successRate(total),
			AvgTps:         avgTps(total),
			Series:         series,
		})
	}

	return QueryResult{
		ModelName:    modelName,
		SeriesSchema: seriesSchema,
		Groups:       results,
	}
}

func bucketPoint(ts int64, value counters) BucketPoint {
	return BucketPoint{
		Ts:             ts,
		RequestCount:   value.requestCount,
		SuccessCount:   value.successCount,
		TotalLatencyMs: value.totalLatencyMs,
		TtftSumMs:      value.ttftSumMs,
		TtftCount:      value.ttftCount,
		OutputTokens:   value.outputTokens,
		GenerationMs:   value.generationMs,
		AvgTtftMs:      avg(value.ttftSumMs, value.ttftCount),
		AvgLatencyMs:   avg(value.totalLatencyMs, value.requestCount),
		SuccessRate:    successRate(value),
		AvgTps:         avgTps(value),
	}
}

func downsampleBucket(ts int64, seconds int64) int64 {
	if seconds <= 0 {
		return ts
	}
	return ts - ts%seconds
}

func buildMatrixCells(merged map[bucketKey]counters) []MatrixCell {
	type cellKey struct{ model, group string }
	byCell := map[cellKey]map[int64]counters{}
	for key, value := range merged {
		ck := cellKey{model: key.model, group: key.group}
		if byCell[ck] == nil {
			byCell[ck] = map[int64]counters{}
		}
		current := byCell[ck][key.bucketTs]
		current.requestCount += value.requestCount
		current.successCount += value.successCount
		current.totalLatencyMs += value.totalLatencyMs
		current.ttftSumMs += value.ttftSumMs
		current.ttftCount += value.ttftCount
		current.outputTokens += value.outputTokens
		current.generationMs += value.generationMs
		byCell[ck][key.bucketTs] = current
	}

	keys := make([]cellKey, 0, len(byCell))
	for key := range byCell {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].group == keys[j].group {
			return keys[i].model < keys[j].model
		}
		return keys[i].group < keys[j].group
	})
	cells := make([]MatrixCell, 0, len(keys))
	for _, key := range keys {
		timestamps := make([]int64, 0, len(byCell[key]))
		for ts := range byCell[key] {
			timestamps = append(timestamps, ts)
		}
		sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
		total := counters{}
		series := make([]MatrixPoint, 0, len(timestamps))
		for _, ts := range timestamps {
			value := byCell[key][ts]
			total.requestCount += value.requestCount
			total.successCount += value.successCount
			total.totalLatencyMs += value.totalLatencyMs
			total.ttftSumMs += value.ttftSumMs
			total.ttftCount += value.ttftCount
			total.outputTokens += value.outputTokens
			total.generationMs += value.generationMs
			series = append(series, MatrixPoint{Ts: ts, RequestCount: value.requestCount,
				SuccessCount: value.successCount, TotalLatencyMs: value.totalLatencyMs,
				TtftSumMs: value.ttftSumMs, TtftCount: value.ttftCount,
				OutputTokens: value.outputTokens, GenerationMs: value.generationMs,
				SuccessRate:  roundedRate(value),
				AvgLatencyMs: avg(value.totalLatencyMs, value.requestCount),
				AvgTtftMs:    avg(value.ttftSumMs, value.ttftCount), AvgTps: roundedTps(value)})
		}
		cells = append(cells, MatrixCell{Model: key.model, Group: key.group,
			RequestCount: total.requestCount, SuccessCount: total.successCount,
			TotalLatencyMs: total.totalLatencyMs, TtftSumMs: total.ttftSumMs,
			TtftCount: total.ttftCount, OutputTokens: total.outputTokens,
			GenerationMs: total.generationMs,
			SuccessRate:  roundedRate(total), AvgLatencyMs: avg(total.totalLatencyMs, total.requestCount),
			AvgTtftMs: avg(total.ttftSumMs, total.ttftCount), AvgTps: roundedTps(total), Series: series})
	}
	return cells
}

func buildChannelMatrixCells(merged map[channelBucketKey]counters) []MatrixCell {
	type cellKey struct {
		channelID    int
		model, group string
	}
	byCell := map[cellKey]map[int64]counters{}
	for key, value := range merged {
		ck := cellKey{channelID: key.channelID, model: key.model, group: key.group}
		if byCell[ck] == nil {
			byCell[ck] = map[int64]counters{}
		}
		current := byCell[ck][key.bucketTs]
		current.requestCount += value.requestCount
		current.successCount += value.successCount
		current.totalLatencyMs += value.totalLatencyMs
		current.ttftSumMs += value.ttftSumMs
		current.ttftCount += value.ttftCount
		current.outputTokens += value.outputTokens
		current.generationMs += value.generationMs
		byCell[ck][key.bucketTs] = current
	}

	keys := make([]cellKey, 0, len(byCell))
	for key := range byCell {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].channelID != keys[j].channelID {
			return keys[i].channelID < keys[j].channelID
		}
		if keys[i].group != keys[j].group {
			return keys[i].group < keys[j].group
		}
		return keys[i].model < keys[j].model
	})

	cells := make([]MatrixCell, 0, len(keys))
	for _, key := range keys {
		timestamps := make([]int64, 0, len(byCell[key]))
		for ts := range byCell[key] {
			timestamps = append(timestamps, ts)
		}
		sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
		total := counters{}
		series := make([]MatrixPoint, 0, len(timestamps))
		for _, ts := range timestamps {
			value := byCell[key][ts]
			total.requestCount += value.requestCount
			total.successCount += value.successCount
			total.totalLatencyMs += value.totalLatencyMs
			total.ttftSumMs += value.ttftSumMs
			total.ttftCount += value.ttftCount
			total.outputTokens += value.outputTokens
			total.generationMs += value.generationMs
			series = append(series, MatrixPoint{
				Ts: ts, RequestCount: value.requestCount, SuccessCount: value.successCount,
				TotalLatencyMs: value.totalLatencyMs, TtftSumMs: value.ttftSumMs,
				TtftCount: value.ttftCount, OutputTokens: value.outputTokens,
				GenerationMs: value.generationMs, SuccessRate: roundedRate(value),
				AvgLatencyMs: avg(value.totalLatencyMs, value.requestCount),
				AvgTtftMs:    avg(value.ttftSumMs, value.ttftCount), AvgTps: roundedTps(value),
			})
		}
		cells = append(cells, MatrixCell{
			ChannelID: key.channelID, Model: key.model, Group: key.group,
			RequestCount: total.requestCount, SuccessCount: total.successCount,
			TotalLatencyMs: total.totalLatencyMs, TtftSumMs: total.ttftSumMs,
			TtftCount: total.ttftCount, OutputTokens: total.outputTokens,
			GenerationMs: total.generationMs, SuccessRate: roundedRate(total),
			AvgLatencyMs: avg(total.totalLatencyMs, total.requestCount),
			AvgTtftMs:    avg(total.ttftSumMs, total.ttftCount), AvgTps: roundedTps(total),
			Series: series,
		})
	}
	return cells
}

// AggregateMatrixCells preserves each metric's own numerator and denominator
// when several internal model/group cells share one public alias pair.
func AggregateMatrixCells(cells []MatrixCell) (MatrixCell, bool) {
	if len(cells) == 0 {
		return MatrixCell{}, false
	}
	result := MatrixCell{ChannelID: cells[0].ChannelID, Model: cells[0].Model, Group: cells[0].Group}
	series := map[int64]counters{}
	for _, cell := range cells {
		result.RequestCount += cell.RequestCount
		result.SuccessCount += cell.SuccessCount
		result.TotalLatencyMs += cell.TotalLatencyMs
		result.TtftSumMs += cell.TtftSumMs
		result.TtftCount += cell.TtftCount
		result.OutputTokens += cell.OutputTokens
		result.GenerationMs += cell.GenerationMs
		for _, point := range cell.Series {
			value := series[point.Ts]
			value.requestCount += point.RequestCount
			value.successCount += point.SuccessCount
			value.totalLatencyMs += point.TotalLatencyMs
			value.ttftSumMs += point.TtftSumMs
			value.ttftCount += point.TtftCount
			value.outputTokens += point.OutputTokens
			value.generationMs += point.GenerationMs
			series[point.Ts] = value
		}
	}
	result.SuccessRate = roundedRate(counters{requestCount: result.RequestCount, successCount: result.SuccessCount})
	result.AvgLatencyMs = avg(result.TotalLatencyMs, result.RequestCount)
	result.AvgTtftMs = avg(result.TtftSumMs, result.TtftCount)
	result.AvgTps = roundedTps(counters{outputTokens: result.OutputTokens, generationMs: result.GenerationMs})
	timestamps := make([]int64, 0, len(series))
	for ts := range series {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	result.Series = make([]MatrixPoint, 0, len(timestamps))
	for _, ts := range timestamps {
		value := series[ts]
		result.Series = append(result.Series, MatrixPoint{
			Ts: ts, RequestCount: value.requestCount, SuccessCount: value.successCount,
			TotalLatencyMs: value.totalLatencyMs, TtftSumMs: value.ttftSumMs,
			TtftCount: value.ttftCount, OutputTokens: value.outputTokens,
			GenerationMs: value.generationMs, SuccessRate: roundedRate(value),
			AvgLatencyMs: avg(value.totalLatencyMs, value.requestCount),
			AvgTtftMs:    avg(value.ttftSumMs, value.ttftCount), AvgTps: roundedTps(value),
		})
	}
	return result, true
}

func roundedRate(value counters) float64 { return math.Round(successRate(value)*100) / 100 }
func roundedTps(value counters) float64  { return math.Round(avgTps(value)*100) / 100 }

func mergeRedisMatrixCurrent(merged map[bucketKey]counters, params MatrixParams, currentBucket int64) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	allowedModels := allowedGroupSet(params.Models)
	allowedGroups := allowedGroupSet(params.Groups)
	if (allowedModels != nil && len(allowedModels) == 0) || (allowedGroups != nil && len(allowedGroups) == 0) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	activeKeys := redisActiveKeys(ctx, currentBucket)
	pipe := common.RDB.Pipeline()
	type pending struct {
		key bucketKey
		cmd *redis.StringStringMapCmd
	}
	pendingReads := make([]pending, 0, len(activeKeys))
	for _, key := range activeKeys {
		if allowedModels != nil {
			if _, ok := allowedModels[key.model]; !ok {
				continue
			}
		}
		if allowedGroups != nil {
			if _, ok := allowedGroups[key.group]; !ok {
				continue
			}
		}
		pendingReads = append(pendingReads, pending{key: key, cmd: pipe.HGetAll(ctx, redisBucketKey(key))})
	}
	_, _ = pipe.Exec(ctx)
	for _, read := range pendingReads {
		values, err := read.cmd.Result()
		if err != nil || len(values) == 0 {
			continue
		}
		read.key.bucketTs = downsampleBucket(read.key.bucketTs, params.BucketSeconds)
		mergeCounters(merged, read.key, redisCounters(values))
	}
}

func mergeRedisChannelMatrixCurrent(merged map[channelBucketKey]counters, params ChannelMatrixParams, currentBucket int64) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	allowedChannels := allowedChannelSet(params.ChannelIDs)
	allowedModels := allowedGroupSet(params.Models)
	allowedGroups := allowedGroupSet(params.Groups)
	if (allowedChannels != nil && len(allowedChannels) == 0) ||
		(allowedModels != nil && len(allowedModels) == 0) ||
		(allowedGroups != nil && len(allowedGroups) == 0) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	activeKeys := redisChannelActiveKeys(ctx, currentBucket)
	pipe := common.RDB.Pipeline()
	type pending struct {
		key channelBucketKey
		cmd *redis.StringStringMapCmd
	}
	reads := make([]pending, 0, len(activeKeys))
	for _, key := range activeKeys {
		if allowedChannels != nil {
			if _, ok := allowedChannels[key.channelID]; !ok {
				continue
			}
		}
		if allowedModels != nil {
			if _, ok := allowedModels[key.model]; !ok {
				continue
			}
		}
		if allowedGroups != nil {
			if _, ok := allowedGroups[key.group]; !ok {
				continue
			}
		}
		reads = append(reads, pending{key: key, cmd: pipe.HGetAll(ctx, redisChannelBucketKey(key))})
	}
	_, _ = pipe.Exec(ctx)
	for _, read := range reads {
		values, err := read.cmd.Result()
		if err != nil || len(values) == 0 {
			continue
		}
		read.key.bucketTs = downsampleBucket(read.key.bucketTs, params.BucketSeconds)
		mergeChannelCounters(merged, read.key, redisCounters(values))
	}
}

func avg(sum int64, count int64) int64 {
	if count <= 0 {
		return 0
	}
	return sum / count
}

func successRate(value counters) float64 {
	if value.requestCount <= 0 {
		return 0
	}
	return float64(value.successCount) / float64(value.requestCount) * 100
}

func avgTps(value counters) float64 {
	if value.outputTokens <= 0 || value.generationMs <= 0 {
		return 0
	}
	return float64(value.outputTokens) / (float64(value.generationMs) / 1000)
}

func recordRedis(key bucketKey, sample Sample) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	redisKey := redisBucketKey(key)
	pipe := common.RDB.TxPipeline()
	pipe.SAdd(ctx, redisActiveBucketKey(key.bucketTs), encodeRedisActiveMember(key))
	pipe.Expire(ctx, redisActiveBucketKey(key.bucketTs), 2*time.Hour)
	pipe.HIncrBy(ctx, redisKey, "req", 1)
	if sample.Success {
		pipe.HIncrBy(ctx, redisKey, "ok", 1)
	}
	if sample.LatencyMs > 0 {
		pipe.HIncrBy(ctx, redisKey, "lat", sample.LatencyMs)
	}
	if sample.HasTtft && sample.TtftMs >= 0 {
		pipe.HIncrBy(ctx, redisKey, "ttft", sample.TtftMs)
		pipe.HIncrBy(ctx, redisKey, "ttft_n", 1)
	}
	if sample.OutputTokens > 0 && sample.GenerationMs > 0 {
		pipe.HIncrBy(ctx, redisKey, "out", sample.OutputTokens)
		pipe.HIncrBy(ctx, redisKey, "gen_ms", sample.GenerationMs)
	}
	pipe.Expire(ctx, redisKey, time.Hour)
	_, _ = pipe.Exec(ctx)
}

func recordChannelRedis(key channelBucketKey, sample Sample) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	redisKey := redisChannelBucketKey(key)
	pipe := common.RDB.TxPipeline()
	pipe.SAdd(ctx, redisChannelActiveBucketKey(key.bucketTs), encodeRedisChannelActiveMember(key))
	pipe.Expire(ctx, redisChannelActiveBucketKey(key.bucketTs), 2*time.Hour)
	pipe.HIncrBy(ctx, redisKey, "req", 1)
	if sample.Success {
		pipe.HIncrBy(ctx, redisKey, "ok", 1)
	}
	if sample.LatencyMs > 0 {
		pipe.HIncrBy(ctx, redisKey, "lat", sample.LatencyMs)
	}
	if sample.HasTtft && sample.TtftMs >= 0 {
		pipe.HIncrBy(ctx, redisKey, "ttft", sample.TtftMs)
		pipe.HIncrBy(ctx, redisKey, "ttft_n", 1)
	}
	if sample.OutputTokens > 0 && sample.GenerationMs > 0 {
		pipe.HIncrBy(ctx, redisKey, "out", sample.OutputTokens)
		pipe.HIncrBy(ctx, redisKey, "gen_ms", sample.GenerationMs)
	}
	pipe.Expire(ctx, redisKey, time.Hour)
	_, _ = pipe.Exec(ctx)
}

func redisBucketKey(key bucketKey) string {
	return fmt.Sprintf("perf:%s:%s:%d", key.model, key.group, key.bucketTs)
}

func redisActiveBucketKey(bucketTs int64) string {
	return fmt.Sprintf("perf:active:%d", bucketTs)
}

func redisChannelBucketKey(key channelBucketKey) string {
	return fmt.Sprintf("perf-channel:v1:%s:%d", encodeRedisChannelActiveMember(key), key.bucketTs)
}

func redisChannelActiveBucketKey(bucketTs int64) string {
	return fmt.Sprintf("perf-channel:v1:active:%d", bucketTs)
}

func encodeRedisChannelActiveMember(key channelBucketKey) string {
	payload := strconv.Itoa(key.channelID) + "\x00" + key.model + "\x00" + key.group
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func decodeRedisChannelActiveMember(member string, bucketTs int64) (channelBucketKey, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(member)
	if err != nil {
		return channelBucketKey{}, false
	}
	parts := strings.SplitN(string(decoded), "\x00", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return channelBucketKey{}, false
	}
	channelID, err := strconv.Atoi(parts[0])
	if err != nil || channelID <= 0 {
		return channelBucketKey{}, false
	}
	return channelBucketKey{channelID: channelID, model: parts[1], group: parts[2], bucketTs: bucketTs}, true
}

func redisChannelActiveKeys(ctx context.Context, bucketTs int64) []channelBucketKey {
	members, err := common.RDB.SMembers(ctx, redisChannelActiveBucketKey(bucketTs)).Result()
	if err != nil {
		return nil
	}
	keys := make([]channelBucketKey, 0, len(members))
	for _, member := range members {
		if key, ok := decodeRedisChannelActiveMember(member, bucketTs); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

func encodeRedisActiveMember(key bucketKey) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key.model + "\x00" + key.group))
}

func decodeRedisActiveMember(member string, bucketTs int64) (bucketKey, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(member)
	if err != nil {
		return bucketKey{}, false
	}
	parts := strings.SplitN(string(decoded), "\x00", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return bucketKey{}, false
	}
	return bucketKey{model: parts[0], group: parts[1], bucketTs: bucketTs}, true
}

func redisActiveKeys(ctx context.Context, bucketTs int64) []bucketKey {
	members, err := common.RDB.SMembers(ctx, redisActiveBucketKey(bucketTs)).Result()
	if err != nil {
		return nil
	}
	keys := make([]bucketKey, 0, len(members))
	for _, member := range members {
		if key, ok := decodeRedisActiveMember(member, bucketTs); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

func mergeRedisQueryActive(merged map[bucketKey]counters, params QueryParams, startTs, endTs int64) {
	active := bucketStart(time.Now().Unix())
	if active < startTs || active > endTs {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	keys := redisActiveKeys(ctx, active)
	pipe := common.RDB.Pipeline()
	type pending struct {
		key bucketKey
		cmd *redis.StringStringMapCmd
	}
	reads := make([]pending, 0, len(keys))
	for _, key := range keys {
		if key.model != params.Model || (params.Group != "" && key.group != params.Group) {
			continue
		}
		reads = append(reads, pending{key: key, cmd: pipe.HGetAll(ctx, redisBucketKey(key))})
	}
	_, _ = pipe.Exec(ctx)
	for _, read := range reads {
		values, err := read.cmd.Result()
		if err == nil && len(values) > 0 {
			mergeCounters(merged, read.key, redisCounters(values))
		}
	}
}

func mergeRedisSummaryActive(totals map[string]counters, modelBuckets map[string]map[int64]counters, allowedGroups map[string]struct{}, startTs, endTs int64) {
	active := bucketStart(time.Now().Unix())
	if active < startTs || active > endTs {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	keys := redisActiveKeys(ctx, active)
	pipe := common.RDB.Pipeline()
	type pending struct {
		key bucketKey
		cmd *redis.StringStringMapCmd
	}
	reads := make([]pending, 0, len(keys))
	for _, key := range keys {
		if allowedGroups != nil {
			if _, ok := allowedGroups[key.group]; !ok {
				continue
			}
		}
		reads = append(reads, pending{key: key, cmd: pipe.HGetAll(ctx, redisBucketKey(key))})
	}
	_, _ = pipe.Exec(ctx)
	for _, read := range reads {
		values, err := read.cmd.Result()
		if err != nil || len(values) == 0 {
			continue
		}
		value := redisCounters(values)
		mergeModelTotals(totals, read.key.model, value)
		mergeModelBucket(modelBuckets, read.key.model, read.key.bucketTs, value)
	}
}
