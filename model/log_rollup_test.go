package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const logRollupTestBase int64 = 1_700_000_040

func setupLogRollupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB := DB
	oldLogDB := LOG_DB
	oldMainType := common.MainDatabaseType()
	oldLogType := common.LogDatabaseType()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	initCol()
	require.NoError(t, db.AutoMigrate(&Log{}))
	require.NoError(t, MigrateLogRollups(context.Background()))

	t.Cleanup(func() {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
		DB = oldDB
		LOG_DB = oldLogDB
		common.SetDatabaseTypes(oldMainType, oldLogType)
		initCol()
	})
	return db
}

func createLogRollupFixtures(t *testing.T, db *gorm.DB, logs []Log) {
	t.Helper()
	for i := range logs {
		require.NoError(t, db.Create(&logs[i]).Error)
	}
}

func finishLogRollupBackfill(t *testing.T, batchSize int, now time.Time) LogRollupRunResult {
	t.Helper()
	var result LogRollupRunResult
	for attempt := 0; attempt < 20; attempt++ {
		var err error
		result, err = RunLogRollupBatch(context.Background(), "test-worker", batchSize, time.Minute, now.Add(time.Duration(attempt)*time.Second))
		require.NoError(t, err)
		if result.CaughtUp {
			return result
		}
	}
	require.FailNow(t, "log rollup backfill did not catch up")
	return result
}

func TestLogRollupAggregatesMatchSupportedFilters(t *testing.T) {
	db := setupLogRollupTestDB(t)
	createLogRollupFixtures(t, db, []Log{
		{UserId: 1, CreatedAt: logRollupTestBase + 1, Type: LogTypeConsume, Username: "alice", TokenName: "token-a", ModelName: "model-alpha", ChannelId: 1, Group: "vip", Quota: 100, PromptTokens: 10, CompletionTokens: 5},
		{UserId: 2, CreatedAt: logRollupTestBase + 2, Type: LogTypeConsume, Username: "bob", TokenName: "token-b", ModelName: "model-beta", ChannelId: 2, Group: "basic", Quota: 200, PromptTokens: 20, CompletionTokens: 10},
		{UserId: 1, CreatedAt: logRollupTestBase + 61, Type: LogTypeManage, Username: "alice", TokenName: "token-a", ModelName: "model-alpha", ChannelId: 1, Group: "vip", Quota: 50, PromptTokens: 5, CompletionTokens: 2},
		{UserId: 1, CreatedAt: logRollupTestBase + 62, Type: LogTypeConsume, Username: "alice", TokenName: "token-a", ModelName: "model-alpha", ChannelId: 1, Group: "vip", Quota: 300, PromptTokens: 30, CompletionTokens: 15},
	})
	now := time.Unix(logRollupTestBase+180, 0)
	result := finishLogRollupBackfill(t, 2, now)
	assert.Equal(t, int64(4), result.WatermarkID)

	baseFilter := LogRollupFilter{StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}
	tests := []struct {
		name   string
		filter LogRollupFilter
		want   int64
	}{
		{name: "global", filter: baseFilter, want: 4},
		{name: "type", filter: LogRollupFilter{LogType: LogTypeConsume, StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}, want: 3},
		{name: "user id", filter: LogRollupFilter{UserID: 1, StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}, want: 3},
		{name: "username", filter: LogRollupFilter{Username: "alice", StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}, want: 3},
		{name: "username wildcard", filter: LogRollupFilter{Username: "%lice", StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}, want: 3},
		{name: "token", filter: LogRollupFilter{TokenName: "token-a", StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}, want: 3},
		{name: "model wildcard", filter: LogRollupFilter{ModelName: "%alpha", StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}, want: 3},
		{name: "channel", filter: LogRollupFilter{ChannelID: 1, StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}, want: 3},
		{name: "group", filter: LogRollupFilter{Group: "vip", StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}, want: 3},
		{name: "combined", filter: LogRollupFilter{LogType: LogTypeConsume, UserID: 1, Username: "alice", TokenName: "token-a", ModelName: "model-alpha", ChannelID: 1, Group: "vip", StartTimestamp: logRollupTestBase, EndTimestamp: logRollupTestBase + 119}, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total, meta, err := QueryLogRollupTotal(context.Background(), tt.filter, now.Add(2*time.Second), 15*time.Second)
			require.NoError(t, err)
			assert.Equal(t, tt.want, total)
			assert.False(t, meta.Stale)
			assert.Equal(t, "minute_rollup", meta.Source)
		})
	}

	stat, _, err := QueryLogRollupStat(context.Background(), baseFilter, now.Add(2*time.Second), 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, LogRollupAggregate{
		RequestCount:     3,
		Quota:            600,
		PromptTokens:     60,
		CompletionTokens: 30,
	}, stat)
}

func TestLogRollupPartialMinuteUsesExactRawRange(t *testing.T) {
	db := setupLogRollupTestDB(t)
	createLogRollupFixtures(t, db, []Log{
		{CreatedAt: logRollupTestBase + 5, Type: LogTypeConsume, Quota: 10},
		{CreatedAt: logRollupTestBase + 30, Type: LogTypeConsume, Quota: 20},
		{CreatedAt: logRollupTestBase + 55, Type: LogTypeConsume, Quota: 30},
		{CreatedAt: logRollupTestBase + 65, Type: LogTypeConsume, Quota: 40},
	})
	now := time.Unix(logRollupTestBase+120, 0)
	finishLogRollupBackfill(t, 10, now)

	filter := LogRollupFilter{StartTimestamp: logRollupTestBase + 20, EndTimestamp: logRollupTestBase + 40}
	total, _, err := QueryLogRollupTotal(context.Background(), filter, now, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	stat, _, err := QueryLogRollupStat(context.Background(), filter, now, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(20), stat.Quota)

	crossMinute := LogRollupFilter{StartTimestamp: logRollupTestBase + 30, EndTimestamp: logRollupTestBase + 70}
	total, _, err = QueryLogRollupTotal(context.Background(), crossMinute, now, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
}

func TestLogRollupBackfillResumesAndRebuildsLateMinute(t *testing.T) {
	db := setupLogRollupTestDB(t)
	logs := make([]Log, 5)
	for i := range logs {
		logs[i] = Log{CreatedAt: logRollupTestBase + int64(i), Type: LogTypeConsume, Quota: 10}
	}
	createLogRollupFixtures(t, db, logs)
	now := time.Unix(logRollupTestBase+120, 0)

	first, err := RunLogRollupBatch(context.Background(), "resume-worker", 2, time.Minute, now)
	require.NoError(t, err)
	assert.False(t, first.BackfillComplete)
	assert.Equal(t, int64(2), first.BackfillCursorID)
	second, err := RunLogRollupBatch(context.Background(), "resume-worker", 2, time.Minute, now.Add(time.Second))
	require.NoError(t, err)
	assert.False(t, second.BackfillComplete)
	assert.Equal(t, int64(4), second.BackfillCursorID)
	third, err := RunLogRollupBatch(context.Background(), "resume-worker", 2, time.Minute, now.Add(2*time.Second))
	require.NoError(t, err)
	assert.True(t, third.BackfillComplete)
	assert.False(t, third.CaughtUp)
	assert.Equal(t, int64(5), third.WatermarkID)

	late := Log{CreatedAt: logRollupTestBase + 2, Type: LogTypeConsume, Quota: 25}
	require.NoError(t, db.Create(&late).Error)
	incremental, err := RunLogRollupBatch(context.Background(), "resume-worker", 2, time.Minute, now.Add(3*time.Second))
	require.NoError(t, err)
	assert.Equal(t, "incremental", incremental.Phase)
	assert.False(t, incremental.CaughtUp)
	assert.Equal(t, int64(late.Id), incremental.WatermarkID)
	caughtUp, err := RunLogRollupBatch(context.Background(), "resume-worker", 2, time.Minute, now.Add(4*time.Second))
	require.NoError(t, err)
	assert.True(t, caughtUp.CaughtUp)

	total, _, err := QueryLogRollupTotal(context.Background(), LogRollupFilter{}, now.Add(5*time.Second), 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(6), total)
	stat, _, err := QueryLogRollupStat(context.Background(), LogRollupFilter{}, now.Add(4*time.Second), 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(75), stat.Quota)
}

func TestLogRollupReconcilesOutOfOrderCommittedID(t *testing.T) {
	setupLogRollupTestDB(t)
	t.Setenv("LOG_ROLLUP_ENABLED", "true")
	require.NoError(t, createLog(&Log{Id: 100, CreatedAt: logRollupTestBase, Type: LogTypeConsume, Quota: 10}))
	require.NoError(t, createLog(&Log{Id: 102, CreatedAt: logRollupTestBase, Type: LogTypeConsume, Quota: 30}))
	now := time.Unix(logRollupTestBase+120, 0)
	finishLogRollupBackfill(t, 10, now)

	require.NoError(t, createLog(&Log{Id: 101, CreatedAt: logRollupTestBase, Type: LogTypeConsume, Quota: 20}))
	var pending int64
	require.NoError(t, LOG_DB.Model(&LogRollupEvent{}).Count(&pending).Error)
	assert.Equal(t, int64(1), pending)
	reconciled, err := RunLogRollupBatch(context.Background(), "reconcile-worker", 10, time.Minute, now.Add(5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, "event_reconcile", reconciled.Phase)
	assert.False(t, reconciled.CaughtUp)
	assert.Equal(t, 1, reconciled.BucketsRebuilt)
	reconciled, err = RunLogRollupBatch(context.Background(), "reconcile-worker", 10, time.Minute, now.Add(6*time.Second))
	require.NoError(t, err)
	assert.True(t, reconciled.CaughtUp)
	require.NoError(t, LOG_DB.Model(&LogRollupEvent{}).Count(&pending).Error)
	assert.Zero(t, pending)

	total, _, err := QueryLogRollupTotal(context.Background(), LogRollupFilter{}, now.Add(7*time.Second), 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	stat, _, err := QueryLogRollupStat(context.Background(), LogRollupFilter{}, now.Add(6*time.Second), 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(60), stat.Quota)
}

func TestLogRollupMarksIncrementalBacklogStaleUntilCaughtUp(t *testing.T) {
	db := setupLogRollupTestDB(t)
	createLogRollupFixtures(t, db, []Log{{CreatedAt: logRollupTestBase, Type: LogTypeConsume}})
	now := time.Unix(logRollupTestBase+120, 0)
	finishLogRollupBackfill(t, 1, now)
	createLogRollupFixtures(t, db, []Log{
		{CreatedAt: logRollupTestBase + 1, Type: LogTypeConsume},
		{CreatedAt: logRollupTestBase + 2, Type: LogTypeConsume},
	})

	first, err := RunLogRollupBatch(context.Background(), "catchup-worker", 1, time.Minute, now.Add(time.Second))
	require.NoError(t, err)
	assert.False(t, first.CaughtUp)
	_, meta, err := QueryLogRollupTotal(context.Background(), LogRollupFilter{}, now.Add(time.Second), 15*time.Second)
	require.NoError(t, err)
	assert.True(t, meta.Stale)

	second, err := RunLogRollupBatch(context.Background(), "catchup-worker", 1, time.Minute, now.Add(2*time.Second))
	require.NoError(t, err)
	assert.False(t, second.CaughtUp)
	final, err := RunLogRollupBatch(context.Background(), "catchup-worker", 1, time.Minute, now.Add(3*time.Second))
	require.NoError(t, err)
	assert.True(t, final.CaughtUp)
	_, meta, err = QueryLogRollupTotal(context.Background(), LogRollupFilter{}, now.Add(3*time.Second), 15*time.Second)
	require.NoError(t, err)
	assert.False(t, meta.Stale)
}

func TestLogRollupPreservesLongPostgreSQLDimensions(t *testing.T) {
	db := setupLogRollupTestDB(t)
	longValue := strings.Repeat("dimension-", 30)
	createLogRollupFixtures(t, db, []Log{{
		CreatedAt: logRollupTestBase,
		Type:      LogTypeConsume,
		Username:  longValue,
		TokenName: longValue,
		ModelName: longValue,
		Group:     longValue,
	}})
	now := time.Unix(logRollupTestBase+120, 0)
	finishLogRollupBackfill(t, 10, now)

	total, _, err := QueryLogRollupTotal(context.Background(), LogRollupFilter{
		Username:  longValue,
		TokenName: longValue,
		ModelName: longValue,
		Group:     longValue,
	}, now, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

func TestCreateLogAndRollupEventAreAtomic(t *testing.T) {
	db := setupLogRollupTestDB(t)
	t.Setenv("LOG_ROLLUP_ENABLED", "true")
	require.NoError(t, db.Migrator().DropTable(&LogRollupEvent{}))

	err := createLog(&Log{CreatedAt: logRollupTestBase, Type: LogTypeConsume})
	require.Error(t, err)
	var logCount int64
	require.NoError(t, db.Model(&Log{}).Count(&logCount).Error)
	assert.Zero(t, logCount)
}

func TestMigrateLogRollupsCreatesQueueInSeparateLogDatabase(t *testing.T) {
	setupLogRollupTestDB(t)
	logDB, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"_logs?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = logDB
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	initCol()
	require.NoError(t, logDB.AutoMigrate(&Log{}))
	require.NoError(t, MigrateLogRollups(context.Background()))
	assert.True(t, DB.Migrator().HasTable(&LogMinuteRollup{}))
	assert.True(t, logDB.Migrator().HasTable(&LogRollupEvent{}))
	t.Cleanup(func() {
		sqlDB, sqlErr := logDB.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
}

func TestLogRollupLeaseCompetitionAndExpiry(t *testing.T) {
	setupLogRollupTestDB(t)
	now := time.Unix(logRollupTestBase, 0)
	require.NoError(t, AcquireLogRollupLease(context.Background(), "worker-a", now, time.Minute))
	err := AcquireLogRollupLease(context.Background(), "worker-b", now.Add(30*time.Second), time.Minute)
	require.ErrorIs(t, err, ErrLogRollupLeaseHeld)
	require.NoError(t, RenewLogRollupLease(context.Background(), "worker-a", now.Add(40*time.Second), time.Minute))
	err = AcquireLogRollupLease(context.Background(), "worker-b", now.Add(90*time.Second), time.Minute)
	require.ErrorIs(t, err, ErrLogRollupLeaseHeld)
	require.NoError(t, AcquireLogRollupLease(context.Background(), "worker-b", now.Add(101*time.Second), time.Minute))
	require.NoError(t, DB.Create(&Log{CreatedAt: logRollupTestBase, Type: LogTypeConsume}).Error)
	_, err = rebuildLogRollupBuckets(context.Background(), []int64{logRollupTestBase}, "worker-a")
	require.ErrorIs(t, err, ErrLogRollupLeaseHeld)
	require.NoError(t, ReleaseLogRollupLease(context.Background(), "worker-b"))
}

func TestLogRollupPageAnchorPreservesStableMinuteOffset(t *testing.T) {
	db := setupLogRollupTestDB(t)
	createLogRollupFixtures(t, db, []Log{
		{CreatedAt: logRollupTestBase - 5, Type: LogTypeConsume},
		{CreatedAt: logRollupTestBase - 5, Type: LogTypeConsume},
		{CreatedAt: logRollupTestBase + 50, Type: LogTypeConsume},
		{CreatedAt: logRollupTestBase + 50, Type: LogTypeConsume},
		{CreatedAt: logRollupTestBase + 50, Type: LogTypeConsume},
	})
	now := time.Unix(logRollupTestBase+120, 0)
	finishLogRollupBackfill(t, 10, now)
	filter := LogRollupFilter{StartTimestamp: logRollupTestBase - 60, EndTimestamp: logRollupTestBase + 59}

	anchor, _, err := QueryLogRollupPageAnchor(context.Background(), filter, 2, now, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, logRollupTestBase, anchor.BucketStart)
	assert.Equal(t, int64(2), anchor.OffsetWithinBucket)
	assert.Equal(t, int64(0), anchor.RowsBeforeBucket)
	var anchored []Log
	require.NoError(t, db.Where("created_at >= ? AND created_at <= ?", filter.StartTimestamp, anchor.BucketEnd).
		Order("created_at DESC, id DESC").Offset(int(anchor.OffsetWithinBucket)).Limit(2).Find(&anchored).Error)
	require.Len(t, anchored, 2)
	assert.Equal(t, logRollupTestBase+50, anchored[0].CreatedAt)
	assert.Equal(t, logRollupTestBase-5, anchored[1].CreatedAt)

	olderAnchor, _, err := QueryLogRollupPageAnchor(context.Background(), filter, 3, now, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, logRollupTestBase-60, olderAnchor.BucketStart)
	assert.Equal(t, int64(0), olderAnchor.OffsetWithinBucket)
	assert.Equal(t, int64(3), olderAnchor.RowsBeforeBucket)
	_, _, err = QueryLogRollupPageAnchor(context.Background(), filter, 5, now, 15*time.Second)
	require.ErrorIs(t, err, ErrLogRollupOffsetPastEnd)
}

func TestCleanupLogRollupsRebuildsPartialCutoff(t *testing.T) {
	db := setupLogRollupTestDB(t)
	createLogRollupFixtures(t, db, []Log{
		{CreatedAt: logRollupTestBase - 50, Type: LogTypeConsume, Quota: 10},
		{CreatedAt: logRollupTestBase + 10, Type: LogTypeConsume, Quota: 20},
		{CreatedAt: logRollupTestBase + 40, Type: LogTypeConsume, Quota: 30},
		{CreatedAt: logRollupTestBase + 70, Type: LogTypeConsume, Quota: 40},
	})
	now := time.Unix(logRollupTestBase+180, 0)
	finishLogRollupBackfill(t, 10, now)

	require.NoError(t, db.Where("created_at < ?", logRollupTestBase+30).Delete(&Log{}).Error)
	require.NoError(t, CleanupLogRollups(context.Background(), logRollupTestBase+30))
	total, _, err := QueryLogRollupTotal(context.Background(), LogRollupFilter{}, now, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	stat, _, err := QueryLogRollupStat(context.Background(), LogRollupFilter{}, now, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(70), stat.Quota)

	require.NoError(t, db.Where("created_at < ?", logRollupTestBase+60).Delete(&Log{}).Error)
	require.NoError(t, CleanupLogRollups(context.Background(), logRollupTestBase+60))
	total, _, err = QueryLogRollupTotal(context.Background(), LogRollupFilter{}, now, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

func TestLogRollupReadinessStalenessAndClickHouseFallback(t *testing.T) {
	db := setupLogRollupTestDB(t)
	now := time.Unix(logRollupTestBase, 0)
	_, _, err := QueryLogRollupTotal(context.Background(), LogRollupFilter{}, now, 15*time.Second)
	require.ErrorIs(t, err, ErrLogRollupNotReady)

	createLogRollupFixtures(t, db, []Log{{CreatedAt: logRollupTestBase, Type: LogTypeConsume}})
	finishLogRollupBackfill(t, 10, now)
	_, meta, err := QueryLogRollupTotal(context.Background(), LogRollupFilter{}, now.Add(17*time.Second), 15*time.Second)
	require.NoError(t, err)
	assert.True(t, meta.Stale)

	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)
	initCol()
	_, _, err = QueryLogRollupTotal(context.Background(), LogRollupFilter{}, now, 15*time.Second)
	require.ErrorIs(t, err, ErrLogRollupClickHouseFallback)
	_, err = RebuildLogRollupBuckets(context.Background(), []int64{logRollupTestBase})
	require.ErrorIs(t, err, ErrLogRollupClickHouseFallback)
}

func TestLogRollupRejectsInvalidRange(t *testing.T) {
	setupLogRollupTestDB(t)
	filter := LogRollupFilter{StartTimestamp: logRollupTestBase + 10, EndTimestamp: logRollupTestBase}
	_, err := makeLogRollupTimePlan(filter)
	require.ErrorIs(t, err, ErrLogRollupInvalidArgument)
	_, _, err = QueryLogRollupPageAnchor(context.Background(), LogRollupFilter{}, -1, time.Now(), time.Second)
	assert.True(t, errors.Is(err, ErrLogRollupNotReady) || errors.Is(err, ErrLogRollupInvalidArgument), fmt.Sprintf("unexpected error: %v", err))
}
