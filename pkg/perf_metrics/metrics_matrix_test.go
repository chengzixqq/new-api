package perfmetrics

import (
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordRelaySampleExcludesHealthProbe(t *testing.T) {
	hotBuckets.Range(func(key, _ any) bool { hotBuckets.Delete(key); return true })
	hotChannelBuckets.Range(func(key, _ any) bool { hotChannelBuckets.Delete(key); return true })
	info := &relaycommon.RelayInfo{
		IsHealthProbe: true, OriginModelName: "MODEL", UsingGroup: "GROUP", StartTime: time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 84},
	}
	info.MarkChannelAttemptDispatched()
	RecordRelaySample(info, true, 1)
	count := 0
	hotBuckets.Range(func(_, _ any) bool { count++; return true })
	assert.Zero(t, count)
	hotChannelBuckets.Range(func(_, _ any) bool { count++; return true })
	assert.Zero(t, count)
}

func TestBuildMatrixCellsUsesRawWeightedNumerators(t *testing.T) {
	merged := map[bucketKey]counters{
		{model: "MODEL", group: "GROUP", bucketTs: 300}: {
			requestCount: 1, successCount: 0, totalLatencyMs: 100,
			ttftSumMs: 20, ttftCount: 1, outputTokens: 10, generationMs: 1000,
		},
		{model: "MODEL", group: "GROUP", bucketTs: 600}: {
			requestCount: 9, successCount: 9, totalLatencyMs: 4500,
			ttftSumMs: 810, ttftCount: 9, outputTokens: 90, generationMs: 9000,
		},
	}

	cells := buildMatrixCells(merged)
	require.Len(t, cells, 1)
	cell := cells[0]
	assert.Equal(t, int64(10), cell.RequestCount)
	assert.Equal(t, int64(9), cell.SuccessCount)
	assert.Equal(t, 90.0, cell.SuccessRate)
	assert.Equal(t, int64(460), cell.AvgLatencyMs)
	assert.Equal(t, int64(83), cell.AvgTtftMs)
	assert.Equal(t, 10.0, cell.AvgTps)
	require.Len(t, cell.Series, 2)
	assert.Equal(t, int64(1), cell.Series[0].RequestCount)
	assert.Equal(t, int64(9), cell.Series[1].RequestCount)
}

func TestRedisActiveMemberRoundTripPreservesSeparators(t *testing.T) {
	original := bucketKey{model: "provider:model/x", group: "group:paid", bucketTs: 300}
	decoded, ok := decodeRedisActiveMember(encodeRedisActiveMember(original), original.bucketTs)
	require.True(t, ok)
	assert.Equal(t, original, decoded)
}

func TestRedisChannelActiveMemberRoundTripPreservesDimensions(t *testing.T) {
	original := channelBucketKey{channelID: 84, model: "provider:model/x", group: "group:paid", bucketTs: 300}
	decoded, ok := decodeRedisChannelActiveMember(encodeRedisChannelActiveMember(original), original.bucketTs)
	require.True(t, ok)
	assert.Equal(t, original, decoded)
}

func TestChannelRetryAttemptsRemainIndependentFromFinalRequestMetric(t *testing.T) {
	hotBuckets.Range(func(key, _ any) bool { hotBuckets.Delete(key); return true })
	hotChannelBuckets.Range(func(key, _ any) bool { hotChannelBuckets.Delete(key); return true })
	t.Cleanup(func() {
		hotBuckets.Range(func(key, _ any) bool { hotBuckets.Delete(key); return true })
		hotChannelBuckets.Range(func(key, _ any) bool { hotChannelBuckets.Delete(key); return true })
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "MODEL", UsingGroup: "CC-MAX", StartTime: time.Now().Add(-time.Second),
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 84},
	}
	info.MarkChannelAttemptDispatched()
	failedAttempt, ok := info.TakeChannelAttempt()
	require.True(t, ok)
	RecordChannelAttempt(failedAttempt, false, 0)

	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 58}
	info.MarkChannelAttemptDispatched()
	time.Sleep(time.Millisecond)
	RecordRelaySample(info, true, 20)

	channelCounters := map[int]counters{}
	hotChannelBuckets.Range(func(key, value any) bool {
		k := key.(channelBucketKey)
		channelCounters[k.channelID] = value.(*atomicBucket).snapshot()
		return true
	})
	require.Len(t, channelCounters, 2)
	assert.Equal(t, int64(1), channelCounters[84].requestCount)
	assert.Zero(t, channelCounters[84].successCount)
	assert.Equal(t, int64(1), channelCounters[58].requestCount)
	assert.Equal(t, int64(1), channelCounters[58].successCount)
	assert.Equal(t, int64(20), channelCounters[58].outputTokens)

	requestCount := int64(0)
	successCount := int64(0)
	hotBuckets.Range(func(_ any, value any) bool {
		snapshot := value.(*atomicBucket).snapshot()
		requestCount += snapshot.requestCount
		successCount += snapshot.successCount
		return true
	})
	assert.Equal(t, int64(1), requestCount)
	assert.Equal(t, int64(1), successCount)
}

func TestFinalFailureDoesNotDuplicateChannelAttempt(t *testing.T) {
	hotBuckets.Range(func(key, _ any) bool { hotBuckets.Delete(key); return true })
	hotChannelBuckets.Range(func(key, _ any) bool { hotChannelBuckets.Delete(key); return true })
	t.Cleanup(func() {
		hotBuckets.Range(func(key, _ any) bool { hotBuckets.Delete(key); return true })
		hotChannelBuckets.Range(func(key, _ any) bool { hotChannelBuckets.Delete(key); return true })
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "MODEL", UsingGroup: "CC-MAX", StartTime: time.Now().Add(-time.Second),
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 84},
	}
	info.MarkChannelAttemptDispatched()
	attempt, ok := info.TakeChannelAttempt()
	require.True(t, ok)
	RecordChannelAttempt(attempt, false, 0)
	RecordRelaySample(info, false, 0)

	channelRequests := int64(0)
	hotChannelBuckets.Range(func(_ any, value any) bool {
		channelRequests += value.(*atomicBucket).snapshot().requestCount
		return true
	})
	requestFailures := int64(0)
	hotBuckets.Range(func(_ any, value any) bool {
		snapshot := value.(*atomicBucket).snapshot()
		requestFailures += snapshot.requestCount - snapshot.successCount
		return true
	})
	assert.Equal(t, int64(1), channelRequests)
	assert.Equal(t, int64(1), requestFailures)
}

func TestBuildChannelMatrixCellsKeepsChannelsSeparate(t *testing.T) {
	merged := map[channelBucketKey]counters{
		{channelID: 84, model: "MODEL", group: "CC-MAX", bucketTs: 300}: {
			requestCount: 10, successCount: 9, totalLatencyMs: 1000,
		},
		{channelID: 58, model: "MODEL", group: "CC-MAX", bucketTs: 300}: {
			requestCount: 20, successCount: 20, totalLatencyMs: 4000,
		},
	}

	cells := buildChannelMatrixCells(merged)
	require.Len(t, cells, 2)
	assert.Equal(t, 58, cells[0].ChannelID)
	assert.Equal(t, int64(20), cells[0].RequestCount)
	assert.Equal(t, 84, cells[1].ChannelID)
	assert.Equal(t, int64(10), cells[1].RequestCount)

	aggregated, ok := AggregateMatrixCells(cells)
	require.True(t, ok)
	assert.Equal(t, int64(30), aggregated.RequestCount)
	assert.Equal(t, int64(29), aggregated.SuccessCount)
	assert.InDelta(t, 96.67, aggregated.SuccessRate, 0.001)
	assert.Equal(t, int64(166), aggregated.AvgLatencyMs)
}

func TestAggregateMatrixCellsWeightsPublicAliasCollisions(t *testing.T) {
	cells := []MatrixCell{
		{
			Model: "INTERNAL_A", Group: "GROUP_A", RequestCount: 90, SuccessCount: 90,
			TotalLatencyMs: 9000, TtftSumMs: 4500, TtftCount: 90,
			OutputTokens: 900, GenerationMs: 9000,
			Series: []MatrixPoint{{Ts: 300, RequestCount: 90, SuccessCount: 90,
				TotalLatencyMs: 9000, TtftSumMs: 4500, TtftCount: 90,
				OutputTokens: 900, GenerationMs: 9000}},
		},
		{
			Model: "INTERNAL_B", Group: "GROUP_B", RequestCount: 10, SuccessCount: 0,
			TotalLatencyMs: 10_000, TtftSumMs: 5000, TtftCount: 10,
			OutputTokens: 100, GenerationMs: 1000,
			Series: []MatrixPoint{{Ts: 300, RequestCount: 10, SuccessCount: 0,
				TotalLatencyMs: 10_000, TtftSumMs: 5000, TtftCount: 10,
				OutputTokens: 100, GenerationMs: 1000}},
		},
	}

	result, ok := AggregateMatrixCells(cells)
	require.True(t, ok)
	assert.Equal(t, int64(100), result.RequestCount)
	assert.Equal(t, 90.0, result.SuccessRate)
	assert.Equal(t, int64(190), result.AvgLatencyMs)
	assert.Equal(t, int64(95), result.AvgTtftMs)
	assert.Equal(t, 100.0, result.AvgTps)
	require.Len(t, result.Series, 1)
	assert.Equal(t, 90.0, result.Series[0].SuccessRate)
}
