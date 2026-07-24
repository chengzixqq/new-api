package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PerfChannelMetric stores aggregated performance counters for real upstream
// attempts. It is intentionally separate from PerfMetric because the latter
// represents one final user request after all channel retries have completed.
type PerfChannelMetric struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	ChannelId      int    `json:"channel_id" gorm:"uniqueIndex:idx_perf_channel_model_group_bucket,priority:1;index:idx_perf_channel_id"`
	ModelName      string `json:"model_name" gorm:"size:128;uniqueIndex:idx_perf_channel_model_group_bucket,priority:2"`
	Group          string `json:"group" gorm:"column:group;size:64;uniqueIndex:idx_perf_channel_model_group_bucket,priority:3"`
	BucketTs       int64  `json:"bucket_ts" gorm:"uniqueIndex:idx_perf_channel_model_group_bucket,priority:4;index:idx_perf_channel_bucket_ts"`
	RequestCount   int64  `json:"-" gorm:"default:0"`
	SuccessCount   int64  `json:"-" gorm:"default:0"`
	TotalLatencyMs int64  `json:"-" gorm:"default:0"`
	TtftSumMs      int64  `json:"-" gorm:"default:0"`
	TtftCount      int64  `json:"-" gorm:"default:0"`
	OutputTokens   int64  `json:"-" gorm:"default:0"`
	GenerationMs   int64  `json:"-" gorm:"default:0"`
}

func (PerfChannelMetric) TableName() string {
	return "perf_channel_metrics"
}

func UpsertPerfChannelMetric(metric *PerfChannelMetric) error {
	if metric == nil || metric.ChannelId <= 0 || metric.RequestCount == 0 {
		return nil
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "channel_id"},
			{Name: "model_name"},
			{Name: "group"},
			{Name: "bucket_ts"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"request_count":    gorm.Expr("perf_channel_metrics.request_count + ?", metric.RequestCount),
			"success_count":    gorm.Expr("perf_channel_metrics.success_count + ?", metric.SuccessCount),
			"total_latency_ms": gorm.Expr("perf_channel_metrics.total_latency_ms + ?", metric.TotalLatencyMs),
			"ttft_sum_ms":      gorm.Expr("perf_channel_metrics.ttft_sum_ms + ?", metric.TtftSumMs),
			"ttft_count":       gorm.Expr("perf_channel_metrics.ttft_count + ?", metric.TtftCount),
			"output_tokens":    gorm.Expr("perf_channel_metrics.output_tokens + ?", metric.OutputTokens),
			"generation_ms":    gorm.Expr("perf_channel_metrics.generation_ms + ?", metric.GenerationMs),
		}),
	}).Create(metric).Error
}

type PerfChannelMetricMatrixRow struct {
	ChannelId      int    `json:"channel_id"`
	ModelName      string `json:"model_name"`
	Group          string `json:"group" gorm:"column:group_name"`
	BucketTs       int64  `json:"bucket_ts"`
	RequestCount   int64  `json:"request_count"`
	SuccessCount   int64  `json:"success_count"`
	TotalLatencyMs int64  `json:"total_latency_ms"`
	TtftSumMs      int64  `json:"ttft_sum_ms"`
	TtftCount      int64  `json:"ttft_count"`
	OutputTokens   int64  `json:"output_tokens"`
	GenerationMs   int64  `json:"generation_ms"`
}

// GetPerfChannelMetricsMatrix returns raw completed channel-attempt buckets.
// A nil filter selects all values while an explicitly empty slice selects none.
func GetPerfChannelMetricsMatrix(startTs int64, endTs int64, channelIds []int, models []string, groups []string) ([]PerfChannelMetricMatrixRow, error) {
	rows := make([]PerfChannelMetricMatrixRow, 0)
	if channelIds != nil && len(channelIds) == 0 {
		return rows, nil
	}
	if models != nil && len(models) == 0 {
		return rows, nil
	}
	if groups != nil && len(groups) == 0 {
		return rows, nil
	}
	query := DB.Model(&PerfChannelMetric{}).
		Select("channel_id, model_name, "+commonGroupCol+" AS group_name, bucket_ts, request_count, success_count, total_latency_ms, ttft_sum_ms, ttft_count, output_tokens, generation_ms").
		Where("bucket_ts >= ? AND bucket_ts <= ?", startTs, endTs)
	if channelIds != nil {
		query = query.Where("channel_id IN ?", channelIds)
	}
	if models != nil {
		query = query.Where("model_name IN ?", models)
	}
	if groups != nil {
		query = query.Where(commonGroupCol+" IN ?", groups)
	}
	err := query.Order("bucket_ts ASC").Find(&rows).Error
	return rows, err
}

func DeletePerfChannelMetricsBefore(cutoffTs int64) error {
	if cutoffTs <= 0 {
		return nil
	}
	return DB.Where("bucket_ts < ?", cutoffTs).Delete(&PerfChannelMetric{}).Error
}
