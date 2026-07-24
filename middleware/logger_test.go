package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccessLoggerDoesNotWriteQueryCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	previousWriter := gin.DefaultWriter
	common.LogWriterMu.Lock()
	gin.DefaultWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		common.LogWriterMu.Unlock()
	})

	server := gin.New()
	SetUpLogger(server)
	server.GET("/v1/models", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models?key=query-api-key&token=login-token", nil)
	server.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	logOutput := output.String()
	assert.Contains(t, logOutput, "/v1/models")
	assert.NotContains(t, logOutput, "query-api-key")
	assert.NotContains(t, logOutput, "login-token")
	assert.NotContains(t, logOutput, "/v1/models?")
}

func TestAccessLoggerUsesCurrentWriterAfterRotation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var originalOutput bytes.Buffer
	var rotatedOutput bytes.Buffer
	previousWriter := gin.DefaultWriter
	common.LogWriterMu.Lock()
	gin.DefaultWriter = &originalOutput
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		common.LogWriterMu.Unlock()
	})

	server := gin.New()
	SetUpLogger(server)
	server.GET("/health", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	common.LogWriterMu.Lock()
	gin.DefaultWriter = &rotatedOutput
	common.LogWriterMu.Unlock()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	server.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Empty(t, originalOutput.String())
	assert.Contains(t, rotatedOutput.String(), "/health")
}

func TestRecoveryWriterUsesCurrentErrorWriterAfterRotation(t *testing.T) {
	var originalOutput bytes.Buffer
	var rotatedOutput bytes.Buffer
	previousWriter := gin.DefaultErrorWriter
	common.LogWriterMu.Lock()
	gin.DefaultErrorWriter = &originalOutput
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previousWriter
		common.LogWriterMu.Unlock()
	})

	writer := CurrentGinErrorWriter()
	common.LogWriterMu.Lock()
	gin.DefaultErrorWriter = &rotatedOutput
	common.LogWriterMu.Unlock()

	_, err := writer.Write([]byte("panic after rotation"))
	require.NoError(t, err)
	assert.Empty(t, originalOutput.String())
	assert.Contains(t, rotatedOutput.String(), "panic after rotation")
}
