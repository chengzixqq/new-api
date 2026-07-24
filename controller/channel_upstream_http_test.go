package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChannelUpstreamHTTPConfigReturnsMinimalNonSensitiveContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	GetChannelUpstreamHTTPConfig(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	require.NoError(t, common.DecodeJson(recorder.Body, &response))
	require.True(t, response.Success)
	keys := make([]string, 0, len(response.Data))
	for key := range response.Data {
		keys = append(keys, key)
	}
	assert.ElementsMatch(t, []string{
		"sse_max_event_size_mb",
		"upstream_http_mode",
		"http2_connection_pool_size",
		"http1_body_threshold_kib",
		"upstream_http_mode_source",
		"http2_connection_pool_size_source",
		"http1_body_threshold_kib_source",
	}, keys)
	assert.NotContains(t, response.Data, "cache_stats")
	assert.NotContains(t, response.Data, "memory_stats")
	assert.NotContains(t, response.Data, "is_running_in_container")
}
