package model_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
)

func TestResolveUpstreamHTTPConfigPrecedence(t *testing.T) {
	previousGlobal := globalSettings
	previousEnvMode := constant.RelayUpstreamHTTPMode
	previousEnvPoolSize := constant.RelayHTTP2ConnectionPoolSize
	previousEnvThreshold := constant.RelayHTTP1BodyThresholdKiB
	t.Cleanup(func() {
		globalSettings = previousGlobal
		constant.RelayUpstreamHTTPMode = previousEnvMode
		constant.RelayHTTP2ConnectionPoolSize = previousEnvPoolSize
		constant.RelayHTTP1BodyThresholdKiB = previousEnvThreshold
	})

	globalSettings.UpstreamHTTPMode = ""
	globalSettings.HTTP2ConnectionPoolSize = nil
	globalSettings.HTTP1BodyThresholdKiB = nil
	constant.RelayUpstreamHTTPMode = ""
	constant.RelayHTTP2ConnectionPoolSize = 0
	constant.RelayHTTP1BodyThresholdKiB = 0

	resolved := ResolveUpstreamHTTPConfig(dto.ChannelOtherSettings{})
	assert.Equal(t, dto.UpstreamHTTPModeAuto, resolved.Mode)
	assert.Equal(t, dto.DefaultHTTP2ConnectionPoolSize, resolved.HTTP2ConnectionPoolSize)
	assert.Equal(t, dto.DefaultHTTP1BodyThresholdKiB, resolved.HTTP1BodyThresholdKiB)
	assert.Equal(t, UpstreamHTTPConfigSourceDefault, resolved.ModeSource)

	constant.RelayUpstreamHTTPMode = constant.UpstreamHTTPModeHybrid
	constant.RelayHTTP2ConnectionPoolSize = 8
	constant.RelayHTTP1BodyThresholdKiB = 512
	resolved = ResolveUpstreamHTTPConfig(dto.ChannelOtherSettings{})
	assert.Equal(t, dto.UpstreamHTTPModeHybrid, resolved.Mode)
	assert.Equal(t, 8, resolved.HTTP2ConnectionPoolSize)
	assert.Equal(t, 512, resolved.HTTP1BodyThresholdKiB)
	assert.Equal(t, UpstreamHTTPConfigSourceEnvironment, resolved.ModeSource)

	globalSettings.UpstreamHTTPMode = dto.UpstreamHTTPModeAuto
	globalSettings.HTTP2ConnectionPoolSize = common.GetPointer(16)
	globalSettings.HTTP1BodyThresholdKiB = common.GetPointer(1024)
	resolved = ResolveUpstreamHTTPConfig(dto.ChannelOtherSettings{})
	assert.Equal(t, dto.UpstreamHTTPModeAuto, resolved.Mode)
	assert.Equal(t, 16, resolved.HTTP2ConnectionPoolSize)
	assert.Equal(t, 1024, resolved.HTTP1BodyThresholdKiB)
	assert.Equal(t, UpstreamHTTPConfigSourceGlobal, resolved.ModeSource)

	channelMode := dto.UpstreamHTTPModeHybrid
	resolved = ResolveUpstreamHTTPConfig(dto.ChannelOtherSettings{
		UpstreamHTTPMode:        &channelMode,
		HTTP2ConnectionPoolSize: common.GetPointer(32),
		HTTP1BodyThresholdKiB:   common.GetPointer(2048),
	})
	assert.Equal(t, dto.UpstreamHTTPModeHybrid, resolved.Mode)
	assert.Equal(t, 32, resolved.HTTP2ConnectionPoolSize)
	assert.Equal(t, 2048, resolved.HTTP1BodyThresholdKiB)
	assert.Equal(t, UpstreamHTTPConfigSourceChannel, resolved.ModeSource)
	assert.Equal(t, UpstreamHTTPConfigSourceChannel, resolved.HTTP2ConnectionPoolSource)
	assert.Equal(t, UpstreamHTTPConfigSourceChannel, resolved.HTTP1BodyThresholdSource)
}

func TestResolveUpstreamHTTPConfigLegacyForceHTTP1(t *testing.T) {
	previousGlobal := globalSettings
	t.Cleanup(func() { globalSettings = previousGlobal })

	globalSettings.UpstreamHTTPMode = dto.UpstreamHTTPModeHybrid
	resolved := ResolveUpstreamHTTPConfig(dto.ChannelOtherSettings{ForceHTTP1: true})
	assert.Equal(t, dto.UpstreamHTTPModeHTTP1, resolved.Mode)
	assert.Equal(t, UpstreamHTTPConfigSourceLegacy, resolved.ModeSource)

	explicitMode := dto.UpstreamHTTPModeAuto
	resolved = ResolveUpstreamHTTPConfig(dto.ChannelOtherSettings{
		ForceHTTP1:       true,
		UpstreamHTTPMode: &explicitMode,
	})
	assert.Equal(t, dto.UpstreamHTTPModeAuto, resolved.Mode)
	assert.Equal(t, UpstreamHTTPConfigSourceChannel, resolved.ModeSource)
}
