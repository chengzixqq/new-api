package model

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	LogRollupBucketSeconds int64 = 60

	LogRollupScopeGlobal    = "global"
	LogRollupScopeDimension = "dimension"

	logRollupAllTypes            = -1
	logRollupStateKey            = "log-minute-rollup-v1"
	logRollupDefaultBatch        = 5000
	logRollupDefaultReconcileIDs = 100000
)

var (
	ErrLogRollupClickHouseFallback = errors.New("log minute rollups are unavailable for ClickHouse log storage; use the original log query")
	ErrLogRollupNotReady           = errors.New("log minute rollups are not ready")
	ErrLogRollupLeaseHeld          = errors.New("log minute rollup lease is held by another worker")
	ErrLogRollupOffsetPastEnd      = errors.New("log minute rollup offset is past the end of the result set")
	ErrLogRollupInvalidArgument    = errors.New("invalid log minute rollup argument")
)

// LogMinuteRollup stores exact aggregates for one minute. Global rows avoid
// grouping high-cardinality dimensions for unfiltered requests. Dimension rows
// retain the complete dimension tuple so arbitrary supported filters can be
// combined without double counting.
type LogMinuteRollup struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	BucketStart   int64  `json:"bucket_start" gorm:"not null;uniqueIndex:uidx_log_minute_rollup,priority:1;index:idx_log_rollup_scope_type_bucket,priority:3;index:idx_log_rollup_scope_bucket,priority:2;index:idx_log_rollup_user_bucket,priority:3;index:idx_log_rollup_channel_bucket,priority:3"`
	Scope         string `json:"scope" gorm:"type:varchar(16);not null;uniqueIndex:uidx_log_minute_rollup,priority:2;index:idx_log_rollup_scope_type_bucket,priority:1;index:idx_log_rollup_scope_bucket,priority:1;index:idx_log_rollup_user_bucket,priority:1;index:idx_log_rollup_channel_bucket,priority:1"`
	LogType       int    `json:"log_type" gorm:"not null;uniqueIndex:uidx_log_minute_rollup,priority:3;index:idx_log_rollup_scope_type_bucket,priority:2"`
	DimensionHash string `json:"-" gorm:"type:char(64);not null;uniqueIndex:uidx_log_minute_rollup,priority:4"`

	UserID    int    `json:"user_id" gorm:"not null;index:idx_log_rollup_user_bucket,priority:2"`
	Username  string `json:"username" gorm:"type:text;not null"`
	TokenName string `json:"token_name" gorm:"type:text;not null"`
	ModelName string `json:"model_name" gorm:"type:text;not null"`
	ChannelID int    `json:"channel_id" gorm:"not null;index:idx_log_rollup_channel_bucket,priority:2"`
	LogGroup  string `json:"group" gorm:"column:log_group;type:text;not null"`

	RequestCount     int64 `json:"request_count" gorm:"not null"`
	Quota            int64 `json:"quota" gorm:"not null"`
	PromptTokens     int64 `json:"prompt_tokens" gorm:"not null"`
	CompletionTokens int64 `json:"completion_tokens" gorm:"not null"`
	MinLogID         int64 `json:"min_log_id" gorm:"not null"`
	MaxLogID         int64 `json:"max_log_id" gorm:"not null"`
}

func (LogMinuteRollup) TableName() string { return "log_minute_rollups" }

// LogRollupState is a singleton state row. Snapshot and cursor fields make the
// initial backfill resumable, while WatermarkID tracks subsequent inserts,
// including late inserts whose created_at belongs to an older minute.
type LogRollupState struct {
	StateKey             string `json:"state_key" gorm:"column:state_key;type:varchar(64);primaryKey"`
	SnapshotInitialized  bool   `json:"snapshot_initialized" gorm:"not null"`
	SnapshotMaxID        int64  `json:"snapshot_max_id" gorm:"not null"`
	BackfillCursorID     int64  `json:"backfill_cursor_id" gorm:"not null"`
	BackfillComplete     bool   `json:"backfill_complete" gorm:"not null"`
	CaughtUp             bool   `json:"caught_up" gorm:"not null"`
	WatermarkID          int64  `json:"watermark_id" gorm:"not null"`
	ReconcileFingerprint string `json:"reconcile_fingerprint" gorm:"type:char(64);not null"`
	MinBucketStart       int64  `json:"min_bucket_start" gorm:"not null"`
	MaxBucketStart       int64  `json:"max_bucket_start" gorm:"not null"`
	LastRefreshAt        int64  `json:"last_refresh_at" gorm:"not null"`
	LeaseOwner           string `json:"lease_owner" gorm:"type:varchar(191);not null"`
	LeaseUntil           int64  `json:"lease_until" gorm:"not null"`
	UpdatedAt            int64  `json:"updated_at" gorm:"not null"`
}

func (LogRollupState) TableName() string { return "log_rollup_states" }

// LogRollupEvent is committed in the same log-database transaction as its log.
// Workers delete events only after rebuilding the affected minute, so commit
// order cannot make an application-written log fall behind an ID watermark.
type LogRollupEvent struct {
	ID        int64 `json:"id" gorm:"primaryKey"`
	LogID     int64 `json:"log_id" gorm:"not null;uniqueIndex"`
	CreatedAt int64 `json:"created_at" gorm:"not null;index"`
}

func (LogRollupEvent) TableName() string { return "log_rollup_events" }

type LogRollupFilter struct {
	LogType        int
	StartTimestamp int64
	EndTimestamp   int64
	UserID         int
	Username       string
	TokenName      string
	ModelName      string
	ChannelID      int
	Group          string
}

type LogRollupAggregate struct {
	RequestCount     int64 `json:"request_count"`
	Quota            int64 `json:"quota"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

type LogRollupQueryMeta struct {
	UpdatedAt   int64  `json:"updated_at"`
	Stale       bool   `json:"stale"`
	Source      string `json:"source"`
	WatermarkID int64  `json:"watermark_id"`
}

type LogRollupPageAnchor struct {
	BucketStart        int64 `json:"bucket_start"`
	BucketEnd          int64 `json:"bucket_end"`
	OffsetWithinBucket int64 `json:"offset_within_bucket"`
	RowsBeforeBucket   int64 `json:"rows_before_bucket"`
}

type LogRollupRunResult struct {
	Phase            string `json:"phase"`
	RowsScanned      int    `json:"rows_scanned"`
	BucketsRebuilt   int    `json:"buckets_rebuilt"`
	WatermarkID      int64  `json:"watermark_id"`
	BackfillCursorID int64  `json:"backfill_cursor_id"`
	BackfillComplete bool   `json:"backfill_complete"`
	CaughtUp         bool   `json:"caught_up"`
}

type logRollupSource struct {
	ID               int64  `gorm:"column:id"`
	UserID           int    `gorm:"column:user_id"`
	CreatedAt        int64  `gorm:"column:created_at"`
	LogType          int    `gorm:"column:type"`
	Username         string `gorm:"column:username"`
	TokenName        string `gorm:"column:token_name"`
	ModelName        string `gorm:"column:model_name"`
	Quota            int64  `gorm:"column:quota"`
	PromptTokens     int64  `gorm:"column:prompt_tokens"`
	CompletionTokens int64  `gorm:"column:completion_tokens"`
	ChannelID        int    `gorm:"column:channel_id"`
	LogGroup         string `gorm:"column:log_group"`
}

type logRollupBatchRow struct {
	ID        int64 `gorm:"column:id"`
	CreatedAt int64 `gorm:"column:created_at"`
}

type logRollupRowKey struct {
	BucketStart   int64
	Scope         string
	LogType       int
	DimensionHash string
}

type logRollupTimeRange struct {
	Start int64
	End   int64
}

type logRollupTimePlan struct {
	HasFull      bool
	FullStartSet bool
	FullStart    int64
	FullEndSet   bool
	FullEnd      int64
	Partials     []logRollupTimeRange
}

func MigrateLogRollups(ctx context.Context) error {
	if DB == nil {
		return fmt.Errorf("%w: main database is not initialized", ErrLogRollupInvalidArgument)
	}
	if err := DB.WithContext(ctx).AutoMigrate(&LogMinuteRollup{}, &LogRollupState{}); err != nil {
		return err
	}
	if LogRollupsSupported() && LOG_DB != nil {
		return LOG_DB.WithContext(ctx).AutoMigrate(&LogRollupEvent{})
	}
	return nil
}

func LogRollupsSupported() bool {
	return !common.UsingLogDatabase(common.DatabaseTypeClickHouse)
}

func ensureLogRollupSupported() error {
	if !LogRollupsSupported() {
		return ErrLogRollupClickHouseFallback
	}
	if DB == nil || LOG_DB == nil {
		return fmt.Errorf("%w: database is not initialized", ErrLogRollupInvalidArgument)
	}
	return nil
}

func logRollupSourceGroupColumn() string {
	if logGroupCol != "" {
		return logGroupCol
	}
	if common.UsingLogDatabase(common.DatabaseTypePostgreSQL) {
		return `"group"`
	}
	return "`group`"
}

func minuteBucketStart(timestamp int64) int64 {
	return timestamp - timestamp%LogRollupBucketSeconds
}

func normalizeLogRollupBuckets(bucketStarts []int64) []int64 {
	seen := make(map[int64]struct{}, len(bucketStarts))
	for _, bucketStart := range bucketStarts {
		seen[minuteBucketStart(bucketStart)] = struct{}{}
	}
	buckets := make([]int64, 0, len(seen))
	for bucketStart := range seen {
		buckets = append(buckets, bucketStart)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
	return buckets
}

func writeLogRollupHashString(hasher hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(value))
}

func hashLogRollupDimensions(source logRollupSource) string {
	hasher := sha256.New()
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], uint64(int64(source.UserID)))
	_, _ = hasher.Write(number[:])
	writeLogRollupHashString(hasher, source.Username)
	writeLogRollupHashString(hasher, source.TokenName)
	writeLogRollupHashString(hasher, source.ModelName)
	binary.BigEndian.PutUint64(number[:], uint64(int64(source.ChannelID)))
	_, _ = hasher.Write(number[:])
	writeLogRollupHashString(hasher, source.LogGroup)
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func addLogRollupValue(rows map[logRollupRowKey]*LogMinuteRollup, key logRollupRowKey, source logRollupSource) {
	row, ok := rows[key]
	if !ok {
		row = &LogMinuteRollup{
			BucketStart:   key.BucketStart,
			Scope:         key.Scope,
			LogType:       key.LogType,
			DimensionHash: key.DimensionHash,
			UserID:        source.UserID,
			Username:      source.Username,
			TokenName:     source.TokenName,
			ModelName:     source.ModelName,
			ChannelID:     source.ChannelID,
			LogGroup:      source.LogGroup,
			MinLogID:      source.ID,
			MaxLogID:      source.ID,
		}
		if key.Scope == LogRollupScopeGlobal {
			row.UserID = 0
			row.Username = ""
			row.TokenName = ""
			row.ModelName = ""
			row.ChannelID = 0
			row.LogGroup = ""
		}
		rows[key] = row
	}
	row.RequestCount++
	row.Quota += source.Quota
	row.PromptTokens += source.PromptTokens
	row.CompletionTokens += source.CompletionTokens
	if source.ID < row.MinLogID {
		row.MinLogID = source.ID
	}
	if source.ID > row.MaxLogID {
		row.MaxLogID = source.ID
	}
}

func aggregateLogRollupSources(rows map[logRollupRowKey]*LogMinuteRollup, sources []logRollupSource) {
	for _, source := range sources {
		bucketStart := minuteBucketStart(source.CreatedAt)
		addLogRollupValue(rows, logRollupRowKey{
			BucketStart: bucketStart,
			Scope:       LogRollupScopeGlobal,
			LogType:     logRollupAllTypes,
		}, source)
		addLogRollupValue(rows, logRollupRowKey{
			BucketStart: bucketStart,
			Scope:       LogRollupScopeGlobal,
			LogType:     source.LogType,
		}, source)
		addLogRollupValue(rows, logRollupRowKey{
			BucketStart:   bucketStart,
			Scope:         LogRollupScopeDimension,
			LogType:       source.LogType,
			DimensionHash: hashLogRollupDimensions(source),
		}, source)
	}
}

// RebuildLogRollupBuckets regenerates complete minute buckets from the source
// log database. Rebuilding rather than incrementing makes retries, late writes,
// and interrupted backfills idempotent.
func RebuildLogRollupBuckets(ctx context.Context, bucketStarts []int64) (int, error) {
	return rebuildLogRollupBuckets(ctx, bucketStarts, "")
}

func requireLogRollupLeaseOwner(tx *gorm.DB, owner string) error {
	var state LogRollupState
	if err := lockForUpdate(tx).Where("state_key = ?", logRollupStateKey).First(&state).Error; err != nil {
		return err
	}
	if state.LeaseOwner != owner {
		return ErrLogRollupLeaseHeld
	}
	return nil
}

func rebuildLogRollupBuckets(ctx context.Context, bucketStarts []int64, leaseOwner string) (int, error) {
	if err := ensureLogRollupSupported(); err != nil {
		return 0, err
	}
	buckets := normalizeLogRollupBuckets(bucketStarts)
	if len(buckets) == 0 {
		return 0, nil
	}

	rows := make(map[logRollupRowKey]*LogMinuteRollup)
	for startIndex := 0; startIndex < len(buckets); {
		endIndex := startIndex
		for endIndex+1 < len(buckets) && buckets[endIndex+1] == buckets[endIndex]+LogRollupBucketSeconds {
			endIndex++
		}
		var sources []logRollupSource
		selectColumns := "id, user_id, created_at, type, username, token_name, model_name, quota, prompt_tokens, completion_tokens, channel_id, " + logRollupSourceGroupColumn() + " AS log_group"
		err := LOG_DB.WithContext(ctx).Table("logs").Select(selectColumns).
			Where("created_at >= ? AND created_at < ?", buckets[startIndex], buckets[endIndex]+LogRollupBucketSeconds).
			Find(&sources).Error
		if err != nil {
			return 0, err
		}
		aggregateLogRollupSources(rows, sources)
		startIndex = endIndex + 1
	}

	rollups := make([]LogMinuteRollup, 0, len(rows))
	for _, row := range rows {
		rollups = append(rollups, *row)
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if leaseOwner != "" {
			if err := requireLogRollupLeaseOwner(tx, leaseOwner); err != nil {
				return err
			}
		}
		const deleteChunkSize = 500
		for start := 0; start < len(buckets); start += deleteChunkSize {
			end := start + deleteChunkSize
			if end > len(buckets) {
				end = len(buckets)
			}
			if err := tx.Where("bucket_start IN ?", buckets[start:end]).Delete(&LogMinuteRollup{}).Error; err != nil {
				return err
			}
		}
		if len(rollups) == 0 {
			return nil
		}
		return tx.CreateInBatches(&rollups, 500).Error
	})
	if err != nil {
		return 0, err
	}
	return len(buckets), nil
}

func ensureLogRollupState(ctx context.Context) error {
	state := LogRollupState{StateKey: logRollupStateKey}
	return DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error
}

func AcquireLogRollupLease(ctx context.Context, owner string, now time.Time, duration time.Duration) error {
	if err := ensureLogRollupSupported(); err != nil {
		return err
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return fmt.Errorf("%w: lease owner is required", ErrLogRollupInvalidArgument)
	}
	if duration <= 0 {
		duration = time.Minute
	}
	if err := ensureLogRollupState(ctx); err != nil {
		return err
	}
	nowUnix := now.Unix()
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state LogRollupState
		if err := lockForUpdate(tx).Where("state_key = ?", logRollupStateKey).First(&state).Error; err != nil {
			return err
		}
		if state.LeaseOwner != "" && state.LeaseOwner != owner && state.LeaseUntil > nowUnix {
			return ErrLogRollupLeaseHeld
		}
		return tx.Model(&LogRollupState{}).Where("state_key = ?", logRollupStateKey).Updates(map[string]interface{}{
			"lease_owner": owner,
			"lease_until": now.Add(duration).Unix(),
			"updated_at":  nowUnix,
		}).Error
	})
}

func AcquireLogRollupLeaseUntilAvailable(ctx context.Context, owner string, duration time.Duration) error {
	for {
		err := AcquireLogRollupLease(ctx, owner, time.Now(), duration)
		if !errors.Is(err, ErrLogRollupLeaseHeld) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func RenewLogRollupLease(ctx context.Context, owner string, now time.Time, duration time.Duration) error {
	if duration <= 0 {
		duration = time.Minute
	}
	result := DB.WithContext(ctx).Model(&LogRollupState{}).
		Where("state_key = ? AND lease_owner = ?", logRollupStateKey, owner).
		Updates(map[string]interface{}{"lease_until": now.Add(duration).Unix(), "updated_at": now.Unix()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLogRollupLeaseHeld
	}
	return nil
}

func ReleaseLogRollupLease(ctx context.Context, owner string) error {
	if DB == nil {
		return nil
	}
	return DB.WithContext(ctx).Model(&LogRollupState{}).
		Where("state_key = ? AND lease_owner = ?", logRollupStateKey, owner).
		Updates(map[string]interface{}{"lease_owner": "", "lease_until": int64(0)}).Error
}

type logRollupLeaseHeartbeat struct {
	cancel context.CancelFunc
	done   chan struct{}
	errors chan error
	err    error
}

func startLogRollupLeaseHeartbeat(ctx context.Context, owner string, duration time.Duration) *logRollupLeaseHeartbeat {
	heartbeatContext, cancel := context.WithCancel(ctx)
	heartbeat := &logRollupLeaseHeartbeat{
		cancel: cancel,
		done:   make(chan struct{}),
		errors: make(chan error, 1),
	}
	interval := duration / 3
	if interval < time.Second {
		interval = time.Second
	}
	go func() {
		defer close(heartbeat.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatContext.Done():
				return
			case heartbeatNow := <-ticker.C:
				if err := RenewLogRollupLease(heartbeatContext, owner, heartbeatNow, duration); err != nil {
					heartbeat.errors <- err
					return
				}
			}
		}
	}()
	return heartbeat
}

func (heartbeat *logRollupLeaseHeartbeat) Check() error {
	if heartbeat.err != nil {
		return heartbeat.err
	}
	select {
	case heartbeat.err = <-heartbeat.errors:
		return heartbeat.err
	default:
		return nil
	}
}

func (heartbeat *logRollupLeaseHeartbeat) Stop() {
	heartbeat.cancel()
	<-heartbeat.done
}

func loadLogRollupState(ctx context.Context) (LogRollupState, error) {
	var state LogRollupState
	err := DB.WithContext(ctx).Where("state_key = ?", logRollupStateKey).First(&state).Error
	return state, err
}

func updateOwnedLogRollupState(ctx context.Context, owner string, values map[string]interface{}) error {
	result := DB.WithContext(ctx).Model(&LogRollupState{}).
		Where("state_key = ? AND lease_owner = ?", logRollupStateKey, owner).
		Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLogRollupLeaseHeld
	}
	return nil
}

func logRollupBatchBuckets(rows []logRollupBatchRow) []int64 {
	buckets := make([]int64, 0, len(rows))
	for _, row := range rows {
		buckets = append(buckets, minuteBucketStart(row.CreatedAt))
	}
	return normalizeLogRollupBuckets(buckets)
}

func deleteProcessedLogRollupEvents(ctx context.Context, rows []logRollupBatchRow) error {
	if len(rows) == 0 {
		return nil
	}
	logIDs := make([]int64, len(rows))
	for i := range rows {
		logIDs[i] = rows[i].ID
	}
	const deleteChunkSize = 500
	for start := 0; start < len(logIDs); start += deleteChunkSize {
		end := start + deleteChunkSize
		if end > len(logIDs) {
			end = len(logIDs)
		}
		if err := LOG_DB.WithContext(ctx).Where("log_id IN ?", logIDs[start:end]).Delete(&LogRollupEvent{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func updateLogRollupBucketBounds(state LogRollupState, buckets []int64, values map[string]interface{}) {
	if len(buckets) == 0 {
		return
	}
	minBucket := buckets[0]
	maxBucket := buckets[len(buckets)-1]
	if state.MinBucketStart == 0 || minBucket < state.MinBucketStart {
		values["min_bucket_start"] = minBucket
	}
	if state.MaxBucketStart == 0 || maxBucket > state.MaxBucketStart {
		values["max_bucket_start"] = maxBucket
	}
}

// RunLogRollupBatch executes one resumable backfill or incremental batch. The
// caller owns scheduling; running this every five seconds keeps the freshness
// bound without coupling database work to request handlers.
func RunLogRollupBatch(ctx context.Context, owner string, batchSize int, leaseDuration time.Duration, now time.Time) (LogRollupRunResult, error) {
	var result LogRollupRunResult
	if err := ensureLogRollupSupported(); err != nil {
		return result, err
	}
	if batchSize <= 0 {
		batchSize = logRollupDefaultBatch
	}
	if now.IsZero() {
		now = time.Now()
	}
	if leaseDuration <= 0 {
		leaseDuration = time.Minute
	}
	if err := AcquireLogRollupLease(ctx, owner, now, leaseDuration); err != nil {
		return result, err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ReleaseLogRollupLease(releaseCtx, owner)
	}()
	heartbeat := startLogRollupLeaseHeartbeat(ctx, owner, leaseDuration)
	defer heartbeat.Stop()

	state, err := loadLogRollupState(ctx)
	if err != nil {
		return result, err
	}
	nowUnix := now.Unix()
	if !state.SnapshotInitialized {
		var snapshotMaxID int64
		if err := LOG_DB.WithContext(ctx).Table("logs").Select("COALESCE(MAX(id), 0)").Scan(&snapshotMaxID).Error; err != nil {
			return result, err
		}
		values := map[string]interface{}{
			"snapshot_initialized": true,
			"snapshot_max_id":      snapshotMaxID,
			"backfill_complete":    snapshotMaxID == 0,
			"caught_up":            false,
			"watermark_id":         int64(0),
			"updated_at":           nowUnix,
		}
		if snapshotMaxID == 0 {
			values["watermark_id"] = int64(0)
		}
		if err := updateOwnedLogRollupState(ctx, owner, values); err != nil {
			return result, err
		}
		state, err = loadLogRollupState(ctx)
		if err != nil {
			return result, err
		}
	}

	var batch []logRollupBatchRow
	result.Phase = "incremental"
	query := LOG_DB.WithContext(ctx).Table("logs").Select("id, created_at").Order("id ASC").Limit(batchSize)
	if !state.BackfillComplete {
		result.Phase = "backfill"
		query = query.Where("id > ? AND id <= ?", state.BackfillCursorID, state.SnapshotMaxID)
	} else {
		query = query.Where("id > ?", state.WatermarkID)
	}
	if err := query.Find(&batch).Error; err != nil {
		return result, err
	}

	values := map[string]interface{}{"caught_up": false, "updated_at": nowUnix}
	if len(batch) == 0 {
		if !state.BackfillComplete {
			values["backfill_complete"] = true
			values["backfill_cursor_id"] = state.SnapshotMaxID
			values["watermark_id"] = state.SnapshotMaxID
			values["reconcile_fingerprint"] = ""
			state.BackfillComplete = true
			state.BackfillCursorID = state.SnapshotMaxID
			state.WatermarkID = state.SnapshotMaxID
			if err := heartbeat.Check(); err != nil {
				return result, err
			}
			if err := updateOwnedLogRollupState(ctx, owner, values); err != nil {
				return result, err
			}
			result.WatermarkID = state.WatermarkID
			result.BackfillCursorID = state.BackfillCursorID
			result.BackfillComplete = true
			result.CaughtUp = false
			return result, nil
		}

		var pendingEvents []LogRollupEvent
		if err := LOG_DB.WithContext(ctx).Order("id ASC").Limit(batchSize).Find(&pendingEvents).Error; err != nil {
			return result, err
		}
		if len(pendingEvents) > 0 {
			result.Phase = "event_reconcile"
			eventBatch := make([]logRollupBatchRow, len(pendingEvents))
			for i := range pendingEvents {
				eventBatch[i] = logRollupBatchRow{ID: pendingEvents[i].LogID, CreatedAt: pendingEvents[i].CreatedAt}
			}
			buckets := logRollupBatchBuckets(eventBatch)
			rebuilt, rebuildErr := rebuildLogRollupBuckets(ctx, buckets, owner)
			if rebuildErr != nil {
				return result, rebuildErr
			}
			if err := deleteProcessedLogRollupEvents(ctx, eventBatch); err != nil {
				return result, err
			}
			result.RowsScanned = len(eventBatch)
			result.BucketsRebuilt = rebuilt
			updateLogRollupBucketBounds(state, buckets, values)
			if err := heartbeat.Check(); err != nil {
				return result, err
			}
			if err := updateOwnedLogRollupState(ctx, owner, values); err != nil {
				return result, err
			}
			result.WatermarkID = state.WatermarkID
			result.BackfillCursorID = state.BackfillCursorID
			result.BackfillComplete = state.BackfillComplete
			result.CaughtUp = false
			return result, nil
		}

		result.Phase = "reconcile"
		reconcileIDs := common.GetEnvOrDefault("LOG_ROLLUP_RECONCILE_IDS", logRollupDefaultReconcileIDs)
		if reconcileIDs < 1000 {
			reconcileIDs = 1000
		}
		if reconcileIDs < batchSize {
			reconcileIDs = batchSize
		}
		if reconcileIDs > 1000000 {
			reconcileIDs = 1000000
		}
		reconcileStartID := state.WatermarkID - int64(reconcileIDs)
		if reconcileStartID < 0 {
			reconcileStartID = 0
		}
		var reconcileRows []logRollupBatchRow
		if state.WatermarkID > 0 {
			if err := LOG_DB.WithContext(ctx).Table("logs").Select("id, created_at").
				Where("id > ? AND id <= ?", reconcileStartID, state.WatermarkID).
				Order("id ASC").Find(&reconcileRows).Error; err != nil {
				return result, err
			}
		}
		hasher := sha256.New()
		var fingerprintValue [16]byte
		for _, row := range reconcileRows {
			binary.BigEndian.PutUint64(fingerprintValue[:8], uint64(row.ID))
			binary.BigEndian.PutUint64(fingerprintValue[8:], uint64(row.CreatedAt))
			_, _ = hasher.Write(fingerprintValue[:])
		}
		reconcileFingerprint := fmt.Sprintf("%x", hasher.Sum(nil))
		if reconcileFingerprint != state.ReconcileFingerprint {
			buckets := logRollupBatchBuckets(reconcileRows)
			rebuilt, rebuildErr := rebuildLogRollupBuckets(ctx, buckets, owner)
			if rebuildErr != nil {
				return result, rebuildErr
			}
			result.BucketsRebuilt = rebuilt
		}
		result.RowsScanned = len(reconcileRows)
		values["reconcile_fingerprint"] = reconcileFingerprint
		values["last_refresh_at"] = nowUnix
		values["caught_up"] = true
		state.CaughtUp = true
		if err := heartbeat.Check(); err != nil {
			return result, err
		}
		if err := updateOwnedLogRollupState(ctx, owner, values); err != nil {
			return result, err
		}
		result.WatermarkID = state.WatermarkID
		result.BackfillCursorID = state.BackfillCursorID
		result.BackfillComplete = state.BackfillComplete
		result.CaughtUp = state.CaughtUp
		return result, nil
	}

	buckets := logRollupBatchBuckets(batch)
	rebuilt, err := rebuildLogRollupBuckets(ctx, buckets, owner)
	if err != nil {
		return result, err
	}
	lastID := batch[len(batch)-1].ID
	result.RowsScanned = len(batch)
	result.BucketsRebuilt = rebuilt
	result.CaughtUp = false
	if err := deleteProcessedLogRollupEvents(ctx, batch); err != nil {
		return result, err
	}
	if !state.BackfillComplete {
		values["backfill_cursor_id"] = lastID
		state.BackfillCursorID = lastID
		if lastID >= state.SnapshotMaxID || len(batch) < batchSize {
			values["backfill_complete"] = true
			values["backfill_cursor_id"] = state.SnapshotMaxID
			values["watermark_id"] = state.SnapshotMaxID
			state.BackfillComplete = true
			state.BackfillCursorID = state.SnapshotMaxID
			state.WatermarkID = state.SnapshotMaxID
		}
	} else {
		values["watermark_id"] = lastID
		state.WatermarkID = lastID
	}
	updateLogRollupBucketBounds(state, buckets, values)
	if err := heartbeat.Check(); err != nil {
		return result, err
	}
	if err := updateOwnedLogRollupState(ctx, owner, values); err != nil {
		return result, err
	}
	result.WatermarkID = state.WatermarkID
	result.BackfillCursorID = state.BackfillCursorID
	result.BackfillComplete = state.BackfillComplete
	result.CaughtUp = false
	return result, nil
}

func validateLogRollupFilter(filter LogRollupFilter) error {
	if filter.StartTimestamp < 0 || filter.EndTimestamp < 0 {
		return fmt.Errorf("%w: timestamps must be non-negative", ErrLogRollupInvalidArgument)
	}
	if filter.StartTimestamp != 0 && filter.EndTimestamp != 0 && filter.EndTimestamp < filter.StartTimestamp {
		return fmt.Errorf("%w: end timestamp precedes start timestamp", ErrLogRollupInvalidArgument)
	}
	return nil
}

func makeLogRollupTimePlan(filter LogRollupFilter) (logRollupTimePlan, error) {
	if err := validateLogRollupFilter(filter); err != nil {
		return logRollupTimePlan{}, err
	}
	plan := logRollupTimePlan{HasFull: true}
	if filter.StartTimestamp != 0 {
		startBucket := minuteBucketStart(filter.StartTimestamp)
		plan.FullStartSet = true
		plan.FullStart = startBucket
		if filter.StartTimestamp != startBucket {
			partialEnd := startBucket + LogRollupBucketSeconds - 1
			if filter.EndTimestamp != 0 && filter.EndTimestamp < partialEnd {
				partialEnd = filter.EndTimestamp
			}
			plan.Partials = append(plan.Partials, logRollupTimeRange{Start: filter.StartTimestamp, End: partialEnd})
			plan.FullStart += LogRollupBucketSeconds
		}
	}
	if filter.EndTimestamp != 0 {
		endBucket := minuteBucketStart(filter.EndTimestamp)
		plan.FullEndSet = true
		plan.FullEnd = endBucket
		if filter.EndTimestamp != endBucket+LogRollupBucketSeconds-1 {
			partialStart := endBucket
			if filter.StartTimestamp > partialStart {
				partialStart = filter.StartTimestamp
			}
			partial := logRollupTimeRange{Start: partialStart, End: filter.EndTimestamp}
			if len(plan.Partials) == 0 || minuteBucketStart(plan.Partials[0].Start) != endBucket {
				plan.Partials = append(plan.Partials, partial)
			}
			plan.FullEnd -= LogRollupBucketSeconds
		}
	}
	if plan.FullEndSet && plan.FullEnd < 0 {
		plan.HasFull = false
	}
	if plan.FullStartSet && plan.FullEndSet && plan.FullStart > plan.FullEnd {
		plan.HasFull = false
	}
	return plan, nil
}

func logRollupHasDimensions(filter LogRollupFilter) bool {
	return filter.UserID != 0 || filter.Username != "" || filter.TokenName != "" || filter.ModelName != "" || filter.ChannelID != 0 || filter.Group != ""
}

func applyRollupTextFilter(tx *gorm.DB, column string, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if !strings.Contains(value, "%") {
		return tx.Where(column+" = ?", value), nil
	}
	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return nil, err
	}
	return tx.Where(column+" LIKE ? ESCAPE '!'", pattern), nil
}

func applyLogRollupFilter(tx *gorm.DB, filter LogRollupFilter) (*gorm.DB, error) {
	if logRollupHasDimensions(filter) {
		tx = tx.Where("scope = ?", LogRollupScopeDimension)
		if filter.LogType != LogTypeUnknown {
			tx = tx.Where("log_type = ?", filter.LogType)
		}
	} else {
		tx = tx.Where("scope = ?", LogRollupScopeGlobal)
		if filter.LogType == LogTypeUnknown {
			tx = tx.Where("log_type = ?", logRollupAllTypes)
		} else {
			tx = tx.Where("log_type = ?", filter.LogType)
		}
	}
	if filter.UserID != 0 {
		tx = tx.Where("user_id = ?", filter.UserID)
	}
	var err error
	if tx, err = applyRollupTextFilter(tx, "username", filter.Username); err != nil {
		return nil, err
	}
	if filter.TokenName != "" {
		tx = tx.Where("token_name = ?", filter.TokenName)
	}
	if tx, err = applyRollupTextFilter(tx, "model_name", filter.ModelName); err != nil {
		return nil, err
	}
	if filter.ChannelID != 0 {
		tx = tx.Where("channel_id = ?", filter.ChannelID)
	}
	if filter.Group != "" {
		tx = tx.Where("log_group = ?", filter.Group)
	}
	return tx, nil
}

func applyRawLogRollupFilter(tx *gorm.DB, filter LogRollupFilter) (*gorm.DB, error) {
	if filter.LogType != LogTypeUnknown {
		tx = tx.Where("type = ?", filter.LogType)
	}
	if filter.UserID != 0 {
		tx = tx.Where("user_id = ?", filter.UserID)
	}
	var err error
	if tx, err = applyExplicitLogTextFilter(tx, "username", filter.Username); err != nil {
		return nil, err
	}
	if filter.TokenName != "" {
		tx = tx.Where("token_name = ?", filter.TokenName)
	}
	if tx, err = applyExplicitLogTextFilter(tx, "model_name", filter.ModelName); err != nil {
		return nil, err
	}
	if filter.ChannelID != 0 {
		tx = tx.Where("channel_id = ?", filter.ChannelID)
	}
	if filter.Group != "" {
		tx = tx.Where(logRollupSourceGroupColumn()+" = ?", filter.Group)
	}
	return tx, nil
}

func queryFullLogRollupAggregate(ctx context.Context, filter LogRollupFilter, plan logRollupTimePlan) (LogRollupAggregate, error) {
	var aggregate LogRollupAggregate
	if !plan.HasFull {
		return aggregate, nil
	}
	tx := DB.WithContext(ctx).Model(&LogMinuteRollup{}).
		Select("COALESCE(SUM(request_count), 0) AS request_count, COALESCE(SUM(quota), 0) AS quota, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens")
	var err error
	if tx, err = applyLogRollupFilter(tx, filter); err != nil {
		return aggregate, err
	}
	if plan.FullStartSet {
		tx = tx.Where("bucket_start >= ?", plan.FullStart)
	}
	if plan.FullEndSet {
		tx = tx.Where("bucket_start <= ?", plan.FullEnd)
	}
	err = tx.Scan(&aggregate).Error
	return aggregate, err
}

func queryRawLogRollupAggregate(ctx context.Context, filter LogRollupFilter, timeRange logRollupTimeRange) (LogRollupAggregate, error) {
	var aggregate LogRollupAggregate
	tx := LOG_DB.WithContext(ctx).Table("logs").
		Select("COUNT(*) AS request_count, COALESCE(SUM(quota), 0) AS quota, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens").
		Where("created_at >= ? AND created_at <= ?", timeRange.Start, timeRange.End)
	var err error
	if tx, err = applyRawLogRollupFilter(tx, filter); err != nil {
		return aggregate, err
	}
	err = tx.Scan(&aggregate).Error
	return aggregate, err
}

func addLogRollupAggregate(target *LogRollupAggregate, value LogRollupAggregate) {
	target.RequestCount += value.RequestCount
	target.Quota += value.Quota
	target.PromptTokens += value.PromptTokens
	target.CompletionTokens += value.CompletionTokens
}

func getLogRollupQueryMeta(ctx context.Context, now time.Time, staleAfter time.Duration) (LogRollupQueryMeta, error) {
	var meta LogRollupQueryMeta
	if err := ensureLogRollupSupported(); err != nil {
		return meta, err
	}
	state, err := loadLogRollupState(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && !state.BackfillComplete) {
		return meta, ErrLogRollupNotReady
	}
	if err != nil {
		return meta, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	if staleAfter <= 0 {
		staleAfter = 15 * time.Second
	}
	meta.UpdatedAt = state.LastRefreshAt
	meta.Stale = !state.CaughtUp || state.LastRefreshAt == 0 || now.Unix()-state.LastRefreshAt > int64(staleAfter/time.Second)
	meta.Source = "minute_rollup"
	meta.WatermarkID = state.WatermarkID
	return meta, nil
}

func queryLogRollupAggregate(ctx context.Context, filter LogRollupFilter, now time.Time, staleAfter time.Duration) (LogRollupAggregate, LogRollupQueryMeta, error) {
	var aggregate LogRollupAggregate
	meta, err := getLogRollupQueryMeta(ctx, now, staleAfter)
	if err != nil {
		return aggregate, meta, err
	}
	plan, err := makeLogRollupTimePlan(filter)
	if err != nil {
		return aggregate, meta, err
	}
	aggregate, err = queryFullLogRollupAggregate(ctx, filter, plan)
	if err != nil {
		return aggregate, meta, err
	}
	for _, partial := range plan.Partials {
		partialAggregate, queryErr := queryRawLogRollupAggregate(ctx, filter, partial)
		if queryErr != nil {
			return aggregate, meta, queryErr
		}
		addLogRollupAggregate(&aggregate, partialAggregate)
	}
	return aggregate, meta, nil
}

func QueryLogRollupTotal(ctx context.Context, filter LogRollupFilter, now time.Time, staleAfter time.Duration) (int64, LogRollupQueryMeta, error) {
	aggregate, meta, err := queryLogRollupAggregate(ctx, filter, now, staleAfter)
	return aggregate.RequestCount, meta, err
}

// QueryLogRollupStat mirrors the existing usage-stat contract: quota covers
// consume logs regardless of the list's type parameter. Request/token totals
// are returned for validation and future callers; RPM/TPM remain live queries.
func QueryLogRollupStat(ctx context.Context, filter LogRollupFilter, now time.Time, staleAfter time.Duration) (LogRollupAggregate, LogRollupQueryMeta, error) {
	filter.LogType = LogTypeConsume
	return queryLogRollupAggregate(ctx, filter, now, staleAfter)
}

type logRollupBucketCount struct {
	BucketStart  int64 `gorm:"column:bucket_start"`
	RequestCount int64 `gorm:"column:request_count"`
}

func QueryLogRollupPageAnchor(ctx context.Context, filter LogRollupFilter, offset int64, now time.Time, staleAfter time.Duration) (LogRollupPageAnchor, LogRollupQueryMeta, error) {
	var anchor LogRollupPageAnchor
	meta, err := getLogRollupQueryMeta(ctx, now, staleAfter)
	if err != nil {
		return anchor, meta, err
	}
	if offset < 0 {
		return anchor, meta, fmt.Errorf("%w: offset must be non-negative", ErrLogRollupInvalidArgument)
	}
	plan, err := makeLogRollupTimePlan(filter)
	if err != nil {
		return anchor, meta, err
	}
	countsByBucket := make(map[int64]int64)
	if plan.HasFull {
		var counts []logRollupBucketCount
		tx := DB.WithContext(ctx).Model(&LogMinuteRollup{}).
			Select("bucket_start, COALESCE(SUM(request_count), 0) AS request_count").
			Group("bucket_start")
		if tx, err = applyLogRollupFilter(tx, filter); err != nil {
			return anchor, meta, err
		}
		if plan.FullStartSet {
			tx = tx.Where("bucket_start >= ?", plan.FullStart)
		}
		if plan.FullEndSet {
			tx = tx.Where("bucket_start <= ?", plan.FullEnd)
		}
		if err = tx.Find(&counts).Error; err != nil {
			return anchor, meta, err
		}
		for _, count := range counts {
			countsByBucket[count.BucketStart] = count.RequestCount
		}
	}
	for _, partial := range plan.Partials {
		aggregate, queryErr := queryRawLogRollupAggregate(ctx, filter, partial)
		if queryErr != nil {
			return anchor, meta, queryErr
		}
		countsByBucket[minuteBucketStart(partial.Start)] = aggregate.RequestCount
	}
	buckets := make([]int64, 0, len(countsByBucket))
	for bucketStart, count := range countsByBucket {
		if count > 0 {
			buckets = append(buckets, bucketStart)
		}
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] > buckets[j] })
	var rowsBefore int64
	for _, bucketStart := range buckets {
		count := countsByBucket[bucketStart]
		if offset < rowsBefore+count {
			bucketEnd := bucketStart + LogRollupBucketSeconds - 1
			if filter.EndTimestamp != 0 && filter.EndTimestamp < bucketEnd {
				bucketEnd = filter.EndTimestamp
			}
			anchor.BucketStart = bucketStart
			anchor.BucketEnd = bucketEnd
			anchor.OffsetWithinBucket = offset - rowsBefore
			anchor.RowsBeforeBucket = rowsBefore
			return anchor, meta, nil
		}
		rowsBefore += count
	}
	return anchor, meta, ErrLogRollupOffsetPastEnd
}

func CleanupLogRollups(ctx context.Context, targetTimestamp int64) error {
	owner := fmt.Sprintf("log-cleanup:%d", time.Now().UnixNano())
	const cleanupLeaseDuration = 5 * time.Minute
	if err := AcquireLogRollupLeaseUntilAvailable(ctx, owner, cleanupLeaseDuration); err != nil {
		return err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ReleaseLogRollupLease(releaseCtx, owner)
	}()
	heartbeat := startLogRollupLeaseHeartbeat(ctx, owner, cleanupLeaseDuration)
	defer heartbeat.Stop()
	if err := cleanupLogRollups(ctx, targetTimestamp, owner, heartbeat.Check); err != nil {
		return err
	}
	return heartbeat.Check()
}

// cleanupLogRollups must run while the caller owns the shared rollup lease.
// Whole expired minutes are removed directly; a partially expired minute is
// rebuilt from remaining source rows so deleted traffic cannot be reinserted.
func cleanupLogRollups(ctx context.Context, targetTimestamp int64, owner string, checkLease func() error) error {
	if err := ensureLogRollupSupported(); err != nil {
		return err
	}
	if targetTimestamp <= 0 {
		return fmt.Errorf("%w: cleanup timestamp must be positive", ErrLogRollupInvalidArgument)
	}
	if err := checkLease(); err != nil {
		return err
	}
	if err := LOG_DB.WithContext(ctx).Where("created_at < ?", targetTimestamp).Delete(&LogRollupEvent{}).Error; err != nil {
		return err
	}
	if err := checkLease(); err != nil {
		return err
	}
	cutoffBucket := minuteBucketStart(targetTimestamp)
	if err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireLogRollupLeaseOwner(tx, owner); err != nil {
			return err
		}
		return tx.Where("bucket_start < ?", cutoffBucket).Delete(&LogMinuteRollup{}).Error
	}); err != nil {
		return err
	}
	if err := checkLease(); err != nil {
		return err
	}
	if targetTimestamp != cutoffBucket {
		if _, err := rebuildLogRollupBuckets(ctx, []int64{cutoffBucket}, owner); err != nil {
			return err
		}
	}
	if err := checkLease(); err != nil {
		return err
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireLogRollupLeaseOwner(tx, owner); err != nil {
			return err
		}
		var bounds struct {
			MinBucket int64 `gorm:"column:min_bucket"`
			MaxBucket int64 `gorm:"column:max_bucket"`
		}
		if err := tx.Model(&LogMinuteRollup{}).
			Select("COALESCE(MIN(bucket_start), 0) AS min_bucket, COALESCE(MAX(bucket_start), 0) AS max_bucket").
			Scan(&bounds).Error; err != nil {
			return err
		}
		return tx.Model(&LogRollupState{}).Where("state_key = ? AND lease_owner = ?", logRollupStateKey, owner).Updates(map[string]interface{}{
			"min_bucket_start":      bounds.MinBucket,
			"max_bucket_start":      bounds.MaxBucket,
			"reconcile_fingerprint": "",
			"updated_at":            time.Now().Unix(),
		}).Error
	})
}
