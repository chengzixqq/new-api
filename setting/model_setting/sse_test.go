package model_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestGetSSEMaxEventSizeBytesPrecedence(t *testing.T) {
	previousGlobal := globalSettings.SSEMaxEventSizeMB
	previousBytes := constant.StreamingMaxBufferSize
	previousLegacyMB := constant.StreamScannerMaxBufferMB
	t.Cleanup(func() {
		globalSettings.SSEMaxEventSizeMB = previousGlobal
		constant.StreamingMaxBufferSize = previousBytes
		constant.StreamScannerMaxBufferMB = previousLegacyMB
	})

	constant.StreamingMaxBufferSize = 20 << 20
	constant.StreamScannerMaxBufferMB = 18
	globalSettings.SSEMaxEventSizeMB = nil
	assert.Equal(t, 20<<20, GetSSEMaxEventSizeBytes(nil))

	globalMB := 32
	globalSettings.SSEMaxEventSizeMB = &globalMB
	assert.Equal(t, 32<<20, GetSSEMaxEventSizeBytes(nil))

	channelMB := 64
	assert.Equal(t, 64<<20, GetSSEMaxEventSizeBytes(&channelMB))

	channelMB = 8
	assert.Equal(t, 8<<20, GetSSEMaxEventSizeBytes(&channelMB), "channel override may be smaller than the global value")
}

func TestGetSSEMaxEventSizeBytesFallbacks(t *testing.T) {
	previousGlobal := globalSettings.SSEMaxEventSizeMB
	previousBytes := constant.StreamingMaxBufferSize
	previousLegacyMB := constant.StreamScannerMaxBufferMB
	t.Cleanup(func() {
		globalSettings.SSEMaxEventSizeMB = previousGlobal
		constant.StreamingMaxBufferSize = previousBytes
		constant.StreamScannerMaxBufferMB = previousLegacyMB
	})

	invalidGlobal := constant.MaxSSEMaxEventSizeMB + 1
	globalSettings.SSEMaxEventSizeMB = &invalidGlobal
	constant.StreamingMaxBufferSize = 0
	constant.StreamScannerMaxBufferMB = 24
	assert.Equal(t, 24<<20, GetSSEMaxEventSizeBytes(nil))

	constant.StreamScannerMaxBufferMB = 0
	assert.Equal(t, constant.DefaultSSEMaxEventSizeMB<<20, GetSSEMaxEventSizeBytes(nil))
}
