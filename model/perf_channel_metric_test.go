package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerfChannelMetricUpsertAndMatrixFilters(t *testing.T) {
	require.NoError(t, DB.Exec("DELETE FROM perf_channel_metrics").Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM perf_channel_metrics").Error)
	})

	first := &PerfChannelMetric{
		ChannelId: 84, ModelName: "MODEL", Group: "CC-MAX", BucketTs: 300,
		RequestCount: 1, SuccessCount: 0, TotalLatencyMs: 1200,
		TtftSumMs: 300, TtftCount: 1, OutputTokens: 0, GenerationMs: 0,
	}
	second := &PerfChannelMetric{
		ChannelId: 84, ModelName: "MODEL", Group: "CC-MAX", BucketTs: 300,
		RequestCount: 2, SuccessCount: 2, TotalLatencyMs: 800,
		TtftSumMs: 200, TtftCount: 2, OutputTokens: 40, GenerationMs: 2000,
	}
	require.NoError(t, UpsertPerfChannelMetric(first))
	require.NoError(t, UpsertPerfChannelMetric(second))
	require.NoError(t, UpsertPerfChannelMetric(&PerfChannelMetric{
		ChannelId: 58, ModelName: "MODEL", Group: "CC-MAX", BucketTs: 300,
		RequestCount: 1, SuccessCount: 1,
	}))

	rows, err := GetPerfChannelMetricsMatrix(0, 600, []int{84}, []string{"MODEL"}, []string{"CC-MAX"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 84, rows[0].ChannelId)
	assert.Equal(t, int64(3), rows[0].RequestCount)
	assert.Equal(t, int64(2), rows[0].SuccessCount)
	assert.Equal(t, int64(2000), rows[0].TotalLatencyMs)
	assert.Equal(t, int64(500), rows[0].TtftSumMs)
	assert.Equal(t, int64(3), rows[0].TtftCount)
	assert.Equal(t, int64(40), rows[0].OutputTokens)
	assert.Equal(t, int64(2000), rows[0].GenerationMs)

	empty, err := GetPerfChannelMetricsMatrix(0, 600, []int{}, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestDeletePerfChannelMetricsBefore(t *testing.T) {
	require.NoError(t, DB.Exec("DELETE FROM perf_channel_metrics").Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM perf_channel_metrics").Error)
	})
	require.NoError(t, DB.Create([]PerfChannelMetric{
		{ChannelId: 84, ModelName: "MODEL", Group: "CC-MAX", BucketTs: 100, RequestCount: 1},
		{ChannelId: 84, ModelName: "MODEL", Group: "CC-MAX", BucketTs: 200, RequestCount: 1},
	}).Error)

	require.NoError(t, DeletePerfChannelMetricsBefore(200))
	var rows []PerfChannelMetric
	require.NoError(t, DB.Order("bucket_ts ASC").Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(200), rows[0].BucketTs)
}
