package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateOptionRejectsInvalidSSESettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
	}{
		{name: "below minimum", body: `{"key":"global.sse_max_event_size_mb","value":0}`},
		{name: "above maximum", body: `{"key":"global.sse_max_event_size_mb","value":129}`},
		{name: "not an integer", body: `{"key":"global.sse_max_event_size_mb","value":"large"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/option", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			UpdateOption(c)

			require.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
		})
	}
}

func TestUpdateOptionRejectsInvalidUpstreamHTTPSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid mode", body: `{"key":"global.upstream_http_mode","value":"http3"}`},
		{name: "pool below minimum", body: `{"key":"global.http2_connection_pool_size","value":0}`},
		{name: "pool above maximum", body: `{"key":"global.http2_connection_pool_size","value":65}`},
		{name: "threshold below minimum", body: `{"key":"global.http1_body_threshold_kib","value":63}`},
		{name: "threshold above maximum", body: `{"key":"global.http1_body_threshold_kib","value":65537}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/option", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			UpdateOption(c)

			require.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"success":false`)
		})
	}
}
