package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelUpstreamHTTPSettingsValidation(t *testing.T) {
	validMode := dto.UpstreamHTTPModeHybrid
	invalidMode := dto.UpstreamHTTPMode("automatic")
	tests := []struct {
		name     string
		settings dto.ChannelOtherSettings
		wantErr  string
	}{
		{name: "unset inherits defaults"},
		{
			name: "valid boundary values",
			settings: dto.ChannelOtherSettings{
				UpstreamHTTPMode:        &validMode,
				HTTP2ConnectionPoolSize: common.GetPointer(dto.MaxHTTP2ConnectionPoolSize),
				HTTP1BodyThresholdKiB:   common.GetPointer(dto.MinHTTP1BodyThresholdKiB),
			},
		},
		{
			name:     "invalid mode",
			settings: dto.ChannelOtherSettings{UpstreamHTTPMode: &invalidMode},
			wantErr:  "upstream_http_mode",
		},
		{
			name:     "pool below range",
			settings: dto.ChannelOtherSettings{HTTP2ConnectionPoolSize: common.GetPointer(dto.MinHTTP2ConnectionPoolSize - 1)},
			wantErr:  "http2_connection_pool_size",
		},
		{
			name:     "pool above range",
			settings: dto.ChannelOtherSettings{HTTP2ConnectionPoolSize: common.GetPointer(dto.MaxHTTP2ConnectionPoolSize + 1)},
			wantErr:  "http2_connection_pool_size",
		},
		{
			name:     "threshold below range",
			settings: dto.ChannelOtherSettings{HTTP1BodyThresholdKiB: common.GetPointer(dto.MinHTTP1BodyThresholdKiB - 1)},
			wantErr:  "http1_body_threshold_kib",
		},
		{
			name:     "threshold above range",
			settings: dto.ChannelOtherSettings{HTTP1BodyThresholdKiB: common.GetPointer(dto.MaxHTTP1BodyThresholdKiB + 1)},
			wantErr:  "http1_body_threshold_kib",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{}
			channel.SetOtherSettings(tt.settings)

			err := channel.ValidateSettings()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			parsed := channel.GetOtherSettings()
			assert.Equal(t, tt.settings.UpstreamHTTPMode, parsed.UpstreamHTTPMode)
			assert.Equal(t, tt.settings.HTTP2ConnectionPoolSize, parsed.HTTP2ConnectionPoolSize)
			assert.Equal(t, tt.settings.HTTP1BodyThresholdKiB, parsed.HTTP1BodyThresholdKiB)
		})
	}
}
