package model_setting

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
)

type UpstreamHTTPConfigSource string

const (
	UpstreamHTTPConfigSourceChannel     UpstreamHTTPConfigSource = "channel"
	UpstreamHTTPConfigSourceLegacy      UpstreamHTTPConfigSource = "legacy_force_http1"
	UpstreamHTTPConfigSourceGlobal      UpstreamHTTPConfigSource = "global"
	UpstreamHTTPConfigSourceEnvironment UpstreamHTTPConfigSource = "environment"
	UpstreamHTTPConfigSourceDefault     UpstreamHTTPConfigSource = "default"
)

type ResolvedUpstreamHTTPConfig struct {
	Mode                      dto.UpstreamHTTPMode     `json:"upstream_http_mode"`
	HTTP2ConnectionPoolSize   int                      `json:"http2_connection_pool_size"`
	HTTP1BodyThresholdKiB     int                      `json:"http1_body_threshold_kib"`
	ModeSource                UpstreamHTTPConfigSource `json:"upstream_http_mode_source"`
	HTTP2ConnectionPoolSource UpstreamHTTPConfigSource `json:"http2_connection_pool_size_source"`
	HTTP1BodyThresholdSource  UpstreamHTTPConfigSource `json:"http1_body_threshold_kib_source"`
}

// ResolveUpstreamHTTPConfig applies the fixed precedence used by relay clients:
// channel override, global option, environment variable, then compiled default.
// The legacy force_http1 channel flag remains a channel-level override when the
// channel has no explicit upstream_http_mode.
func ResolveUpstreamHTTPConfig(channel dto.ChannelOtherSettings) ResolvedUpstreamHTTPConfig {
	resolved := ResolvedUpstreamHTTPConfig{
		Mode:                      dto.UpstreamHTTPModeAuto,
		HTTP2ConnectionPoolSize:   dto.DefaultHTTP2ConnectionPoolSize,
		HTTP1BodyThresholdKiB:     dto.DefaultHTTP1BodyThresholdKiB,
		ModeSource:                UpstreamHTTPConfigSourceDefault,
		HTTP2ConnectionPoolSource: UpstreamHTTPConfigSourceDefault,
		HTTP1BodyThresholdSource:  UpstreamHTTPConfigSourceDefault,
	}

	if isValidUpstreamHTTPMode(dto.UpstreamHTTPMode(constant.RelayUpstreamHTTPMode)) {
		resolved.Mode = dto.UpstreamHTTPMode(constant.RelayUpstreamHTTPMode)
		resolved.ModeSource = UpstreamHTTPConfigSourceEnvironment
	}
	if isValidHTTP2ConnectionPoolSize(constant.RelayHTTP2ConnectionPoolSize) {
		resolved.HTTP2ConnectionPoolSize = constant.RelayHTTP2ConnectionPoolSize
		resolved.HTTP2ConnectionPoolSource = UpstreamHTTPConfigSourceEnvironment
	}
	if isValidHTTP1BodyThresholdKiB(constant.RelayHTTP1BodyThresholdKiB) {
		resolved.HTTP1BodyThresholdKiB = constant.RelayHTTP1BodyThresholdKiB
		resolved.HTTP1BodyThresholdSource = UpstreamHTTPConfigSourceEnvironment
	}

	if isValidUpstreamHTTPMode(globalSettings.UpstreamHTTPMode) {
		resolved.Mode = globalSettings.UpstreamHTTPMode
		resolved.ModeSource = UpstreamHTTPConfigSourceGlobal
	}
	if globalSettings.HTTP2ConnectionPoolSize != nil && isValidHTTP2ConnectionPoolSize(*globalSettings.HTTP2ConnectionPoolSize) {
		resolved.HTTP2ConnectionPoolSize = *globalSettings.HTTP2ConnectionPoolSize
		resolved.HTTP2ConnectionPoolSource = UpstreamHTTPConfigSourceGlobal
	}
	if globalSettings.HTTP1BodyThresholdKiB != nil && isValidHTTP1BodyThresholdKiB(*globalSettings.HTTP1BodyThresholdKiB) {
		resolved.HTTP1BodyThresholdKiB = *globalSettings.HTTP1BodyThresholdKiB
		resolved.HTTP1BodyThresholdSource = UpstreamHTTPConfigSourceGlobal
	}

	if channel.HTTP2ConnectionPoolSize != nil && isValidHTTP2ConnectionPoolSize(*channel.HTTP2ConnectionPoolSize) {
		resolved.HTTP2ConnectionPoolSize = *channel.HTTP2ConnectionPoolSize
		resolved.HTTP2ConnectionPoolSource = UpstreamHTTPConfigSourceChannel
	}
	if channel.HTTP1BodyThresholdKiB != nil && isValidHTTP1BodyThresholdKiB(*channel.HTTP1BodyThresholdKiB) {
		resolved.HTTP1BodyThresholdKiB = *channel.HTTP1BodyThresholdKiB
		resolved.HTTP1BodyThresholdSource = UpstreamHTTPConfigSourceChannel
	}

	if channel.UpstreamHTTPMode != nil && isValidUpstreamHTTPMode(*channel.UpstreamHTTPMode) {
		resolved.Mode = *channel.UpstreamHTTPMode
		resolved.ModeSource = UpstreamHTTPConfigSourceChannel
	} else if channel.ForceHTTP1 {
		resolved.Mode = dto.UpstreamHTTPModeHTTP1
		resolved.ModeSource = UpstreamHTTPConfigSourceLegacy
	}

	return resolved
}

func isValidUpstreamHTTPMode(mode dto.UpstreamHTTPMode) bool {
	switch mode {
	case dto.UpstreamHTTPModeAuto, dto.UpstreamHTTPModeHTTP1, dto.UpstreamHTTPModeHybrid:
		return true
	default:
		return false
	}
}

func isValidHTTP2ConnectionPoolSize(value int) bool {
	return value >= dto.MinHTTP2ConnectionPoolSize && value <= dto.MaxHTTP2ConnectionPoolSize
}

func isValidHTTP1BodyThresholdKiB(value int) bool {
	return value >= dto.MinHTTP1BodyThresholdKiB && value <= dto.MaxHTTP1BodyThresholdKiB
}
