package model_setting

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/config"
)

type ChatCompletionsToResponsesPolicy struct {
	Enabled       bool     `json:"enabled"`
	AllChannels   bool     `json:"all_channels"`
	ChannelIDs    []int    `json:"channel_ids,omitempty"`
	ChannelTypes  []int    `json:"channel_types,omitempty"`
	ModelPatterns []string `json:"model_patterns,omitempty"`
}

func (p ChatCompletionsToResponsesPolicy) IsChannelEnabled(channelID int, channelType int) bool {
	if !p.Enabled {
		return false
	}
	if p.AllChannels {
		return true
	}

	if channelID > 0 && len(p.ChannelIDs) > 0 && slices.Contains(p.ChannelIDs, channelID) {
		return true
	}
	if channelType > 0 && len(p.ChannelTypes) > 0 && slices.Contains(p.ChannelTypes, channelType) {
		return true
	}
	return false
}

type GlobalSettings struct {
	PassThroughRequestEnabled        bool                             `json:"pass_through_request_enabled"`
	ThinkingModelBlacklist           []string                         `json:"thinking_model_blacklist"`
	ChatCompletionsToResponsesPolicy ChatCompletionsToResponsesPolicy `json:"chat_completions_to_responses_policy"`
	SSEMaxEventSizeMB                *int                             `json:"sse_max_event_size_mb"`
	UpstreamHTTPMode                 dto.UpstreamHTTPMode             `json:"upstream_http_mode"`
	HTTP2ConnectionPoolSize          *int                             `json:"http2_connection_pool_size"`
	HTTP1BodyThresholdKiB            *int                             `json:"http1_body_threshold_kib"`
}

// 默认配置
var defaultOpenaiSettings = GlobalSettings{
	PassThroughRequestEnabled: false,
	ThinkingModelBlacklist: []string{
		"moonshotai/kimi-k2-thinking",
		"kimi-k2-thinking",
	},
	ChatCompletionsToResponsesPolicy: ChatCompletionsToResponsesPolicy{
		Enabled:     false,
		AllChannels: true,
	},
}

// 全局实例
var globalSettings = defaultOpenaiSettings

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("global", &globalSettings)
}

func GetGlobalSettings() *GlobalSettings {
	return &globalSettings
}

// GetSSEMaxEventSizeBytes resolves the maximum size of one upstream SSE event.
// A channel override is intentionally allowed to be either smaller or larger
// than the system-wide value.
func GetSSEMaxEventSizeBytes(channelOverride *int) int {
	if channelOverride != nil && *channelOverride >= constant.MinSSEMaxEventSizeMB && *channelOverride <= constant.MaxSSEMaxEventSizeMB {
		return *channelOverride << 20
	}
	if globalSettings.SSEMaxEventSizeMB != nil && *globalSettings.SSEMaxEventSizeMB >= constant.MinSSEMaxEventSizeMB && *globalSettings.SSEMaxEventSizeMB <= constant.MaxSSEMaxEventSizeMB {
		return *globalSettings.SSEMaxEventSizeMB << 20
	}
	if constant.StreamingMaxBufferSize >= constant.MinSSEMaxEventSizeMB<<20 && constant.StreamingMaxBufferSize <= constant.MaxSSEMaxEventSizeMB<<20 {
		return constant.StreamingMaxBufferSize
	}
	if constant.StreamScannerMaxBufferMB >= constant.MinSSEMaxEventSizeMB && constant.StreamScannerMaxBufferMB <= constant.MaxSSEMaxEventSizeMB {
		return constant.StreamScannerMaxBufferMB << 20
	}
	return constant.DefaultSSEMaxEventSizeMB << 20
}

// ShouldPreserveThinkingSuffix 判断模型是否配置为保留 thinking/-nothinking/-low/-high/-medium 后缀
func ShouldPreserveThinkingSuffix(modelName string) bool {
	target := strings.TrimSpace(modelName)
	if target == "" {
		return false
	}

	for _, entry := range globalSettings.ThinkingModelBlacklist {
		if strings.TrimSpace(entry) == target {
			return true
		}
	}
	return false
}
