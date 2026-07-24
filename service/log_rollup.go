package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	defaultLogRollupRefreshSeconds = 5
	defaultLogRollupBatchSize      = 5000
	logRollupBackfillYield         = 25 * time.Millisecond
)

type LogRollupConfig struct {
	Enabled         bool
	ReadEnabled     bool
	RefreshInterval time.Duration
	BatchSize       int
	LeaseDuration   time.Duration
}

// GetLogRollupConfig reads startup configuration without introducing mutable
// package globals. Both switches default off so deploying the code alone does
// not change log-query behavior or start a historical backfill.
func GetLogRollupConfig() LogRollupConfig {
	refreshSeconds := common.GetEnvOrDefault("LOG_ROLLUP_REFRESH_SECONDS", defaultLogRollupRefreshSeconds)
	if refreshSeconds < 1 {
		refreshSeconds = 1
	}
	batchSize := common.GetEnvOrDefault("LOG_ROLLUP_BATCH_SIZE", defaultLogRollupBatchSize)
	if batchSize < 1 {
		batchSize = defaultLogRollupBatchSize
	}
	refreshInterval := time.Duration(refreshSeconds) * time.Second
	leaseDuration := 3 * refreshInterval
	if leaseDuration < time.Minute {
		leaseDuration = time.Minute
	}
	return LogRollupConfig{
		Enabled:         common.GetEnvOrDefaultBool("LOG_ROLLUP_ENABLED", false),
		ReadEnabled:     common.GetEnvOrDefaultBool("LOG_ROLLUP_READ_ENABLED", false),
		RefreshInterval: refreshInterval,
		BatchSize:       batchSize,
		LeaseDuration:   leaseDuration,
	}
}

func LogRollupReadEnabled() bool {
	config := GetLogRollupConfig()
	return config.Enabled && config.ReadEnabled && model.LogRollupsSupported()
}

type LogRollupWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StartLogRollupWorker starts one cancellable worker. During initial backfill
// it immediately claims the next batch after a short scheduler yield; once
// caught up it polls at RefreshInterval.
func StartLogRollupWorker(parent context.Context) *LogRollupWorker {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	worker := &LogRollupWorker{cancel: cancel, done: make(chan struct{})}
	config := GetLogRollupConfig()
	if !config.Enabled || !model.LogRollupsSupported() {
		close(worker.done)
		return worker
	}

	hostname, _ := os.Hostname()
	owner := fmt.Sprintf("%s:%d:%d", hostname, os.Getpid(), time.Now().UnixNano())
	go func() {
		defer close(worker.done)
		for {
			result, err := model.RunLogRollupBatch(ctx, owner, config.BatchSize, config.LeaseDuration, time.Now())
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, model.ErrLogRollupLeaseHeld) {
				common.SysError("log minute rollup worker failed: " + err.Error())
			}
			if ctx.Err() != nil {
				return
			}

			wait := config.RefreshInterval
			if err == nil && !result.CaughtUp {
				wait = logRollupBackfillYield
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
		}
	}()
	return worker
}

func (worker *LogRollupWorker) Stop(ctx context.Context) error {
	if worker == nil {
		return nil
	}
	worker.cancel()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-worker.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
