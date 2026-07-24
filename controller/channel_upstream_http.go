package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

// GetChannelUpstreamHTTPConfig exposes only the non-sensitive effective
// transport defaults needed by channel editors. Detailed performance and
// process statistics remain restricted to the root-only performance endpoint.
func GetChannelUpstreamHTTPConfig(c *gin.Context) {
	resolved := model_setting.ResolveUpstreamHTTPConfig(dto.ChannelOtherSettings{})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"sse_max_event_size_mb":             model_setting.GetSSEMaxEventSizeBytes(nil) >> 20,
			"upstream_http_mode":                resolved.Mode,
			"http2_connection_pool_size":        resolved.HTTP2ConnectionPoolSize,
			"http1_body_threshold_kib":          resolved.HTTP1BodyThresholdKiB,
			"upstream_http_mode_source":         resolved.ModeSource,
			"http2_connection_pool_size_source": resolved.HTTP2ConnectionPoolSource,
			"http1_body_threshold_kib_source":   resolved.HTTP1BodyThresholdSource,
		},
	})
}
