package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelTaskPollingSettingsValidation(t *testing.T) {
	tests := []struct {
		name     string
		settings dto.ChannelOtherSettings
		wantErr  string
	}{
		{name: "unset inherits defaults"},
		{
			name: "minimum values",
			settings: dto.ChannelOtherSettings{
				TaskPollingConcurrency: common.GetPointer(dto.MinTaskPollingConcurrency),
				TaskPollingIntervalMs:  common.GetPointer(dto.MinTaskPollingIntervalMs),
			},
		},
		{
			name: "maximum values",
			settings: dto.ChannelOtherSettings{
				TaskPollingConcurrency: common.GetPointer(dto.MaxTaskPollingConcurrency),
				TaskPollingIntervalMs:  common.GetPointer(dto.MaxTaskPollingIntervalMs),
			},
		},
		{
			name: "concurrency below range",
			settings: dto.ChannelOtherSettings{
				TaskPollingConcurrency: common.GetPointer(dto.MinTaskPollingConcurrency - 1),
			},
			wantErr: "task_polling_concurrency",
		},
		{
			name: "concurrency above range",
			settings: dto.ChannelOtherSettings{
				TaskPollingConcurrency: common.GetPointer(dto.MaxTaskPollingConcurrency + 1),
			},
			wantErr: "task_polling_concurrency",
		},
		{
			name: "interval below range",
			settings: dto.ChannelOtherSettings{
				TaskPollingIntervalMs: common.GetPointer(dto.MinTaskPollingIntervalMs - 1),
			},
			wantErr: "task_polling_interval_ms",
		},
		{
			name: "interval above range",
			settings: dto.ChannelOtherSettings{
				TaskPollingIntervalMs: common.GetPointer(dto.MaxTaskPollingIntervalMs + 1),
			},
			wantErr: "task_polling_interval_ms",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{}
			channel.SetOtherSettings(tt.settings)

			err := channel.ValidateSettings()
			if tt.wantErr == "" {
				require.NoError(t, err)
				parsed := channel.GetOtherSettings()
				assert.Equal(t, tt.settings.TaskPollingConcurrency, parsed.TaskPollingConcurrency)
				assert.Equal(t, tt.settings.TaskPollingIntervalMs, parsed.TaskPollingIntervalMs)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
