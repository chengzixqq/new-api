package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoMidjourneyHTTPRequestUsesChannelTransportPolicy(t *testing.T) {
	initializeUpstreamHTTPClientsForTest(t)

	protocol := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		protocol <- request.Proto
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{}`)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "http://downstream.example/mj/submit", strings.NewReader(`{"prompt":"test"}`))
	context.Request.Header.Set("Content-Type", "application/json")
	mode := dto.UpstreamHTTPModeHTTP1
	common.SetContextKey(context, constant.ContextKeyChannelId, 501)
	common.SetContextKey(context, constant.ContextKeyChannelSetting, dto.ChannelSettings{})
	common.SetContextKey(context, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{UpstreamHTTPMode: &mode})

	_, _, err := DoMidjourneyHttpRequest(context, time.Second, server.URL)
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1", <-protocol)
}
