package model

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAllLogsUsesRollupTotalAndDeepPageAnchor(t *testing.T) {
	db := setupLogRollupTestDB(t)
	createLogRollupFixtures(t, db, []Log{
		{CreatedAt: logRollupTestBase + 90, Type: LogTypeConsume, RequestId: "request-5"},
		{CreatedAt: logRollupTestBase + 70, Type: LogTypeConsume, RequestId: "request-4"},
		{CreatedAt: logRollupTestBase + 50, Type: LogTypeConsume, RequestId: "request-3"},
		{CreatedAt: logRollupTestBase + 50, Type: LogTypeConsume, RequestId: "request-2"},
		{CreatedAt: logRollupTestBase + 10, Type: LogTypeConsume, RequestId: "request-1"},
	})
	now := time.Unix(logRollupTestBase+180, 0)
	finishLogRollupBackfill(t, 10, now)
	options := LogQueryOptions{
		Context:    context.Background(),
		UseRollup:  true,
		Now:        now.Add(time.Second),
		StaleAfter: 15 * time.Second,
	}

	logs, total, meta, err := GetAllLogs(
		LogTypeUnknown,
		logRollupTestBase,
		logRollupTestBase+119,
		"",
		"",
		"",
		2,
		2,
		0,
		"",
		"",
		"",
		options,
	)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, int64(5), total)
	assert.Equal(t, "minute_rollup", meta.Source)
	assert.Equal(t, "request-2", logs[0].RequestId)
	assert.Equal(t, "request-3", logs[1].RequestId)

	logs, total, meta, err = GetAllLogs(
		LogTypeUnknown,
		logRollupTestBase,
		logRollupTestBase+119,
		"",
		"",
		"",
		0,
		10,
		0,
		"",
		"request-4",
		"",
		options,
	)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "raw", meta.Source)
}

func TestSumUsedQuotaUsesRollupAndKeepsLiveRateMetrics(t *testing.T) {
	db := setupLogRollupTestDB(t)
	now := time.Now().Truncate(time.Second)
	createLogRollupFixtures(t, db, []Log{
		{CreatedAt: now.Unix() - 10, Type: LogTypeConsume, Quota: 100, PromptTokens: 10, CompletionTokens: 5},
		{CreatedAt: now.Unix() - 5, Type: LogTypeConsume, Quota: 200, PromptTokens: 20, CompletionTokens: 10},
		{CreatedAt: now.Unix() - 5, Type: LogTypeManage, Quota: 900, PromptTokens: 90, CompletionTokens: 90},
	})
	finishLogRollupBackfill(t, 10, now)
	options := LogQueryOptions{
		Context:    context.Background(),
		UseRollup:  true,
		Now:        now.Add(time.Second),
		StaleAfter: 15 * time.Second,
	}

	stat, err := SumUsedQuota(
		LogTypeUnknown,
		now.Add(-time.Minute).Unix(),
		now.Add(time.Minute).Unix(),
		"",
		"",
		"",
		0,
		"",
		options,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(300), stat.Quota)
	assert.Equal(t, int64(2), stat.Rpm)
	assert.Equal(t, int64(45), stat.Tpm)
	assert.Equal(t, "minute_rollup", stat.Source)
	assert.False(t, stat.Stale)

	stat, err = SumUsedQuota(
		LogTypeUnknown,
		now.Add(-time.Minute).Unix(),
		now.Add(time.Minute).Unix(),
		"",
		"",
		"",
		0,
		"",
		LogQueryOptions{Context: context.Background()},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(300), stat.Quota)
	assert.Equal(t, "raw", stat.Source)
}
