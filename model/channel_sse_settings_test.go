package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

func TestChannelValidateSettingsSSEMaxEventSize(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{name: "minimum", value: constant.MinSSEMaxEventSizeMB},
		{name: "maximum", value: constant.MaxSSEMaxEventSizeMB},
		{name: "below minimum", value: constant.MinSSEMaxEventSizeMB - 1, wantErr: true},
		{name: "above maximum", value: constant.MaxSSEMaxEventSizeMB + 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setting := fmt.Sprintf(`{"sse_max_event_size_mb":%d}`, tt.value)
			channel := Channel{Setting: &setting}
			err := channel.ValidateSettings()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
