package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reasoningOnlyResponsesBody = `{"id":"resp_empty","object":"response","status":"completed","model":"gpt-test","output":[{"type":"reasoning","content":[],"encrypted_content":"must-not-leak","summary":[]}],"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`

func TestOaiResponsesHandlerRejectsReasoningOnlyCompletedResponse(t *testing.T) {
	ctx, recorder := newResponsesTestContext()
	enabled := true
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelSetting: dto.ChannelSettings{ResponsesEmptyOutputGuard: &enabled},
		},
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(reasoningOnlyResponsesBody)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesHandler(ctx, info, resp)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeUpstreamEmptyResponse, apiErr.GetErrorCode())
	assert.Equal(t, http.StatusFailedDependency, apiErr.StatusCode)
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Equal(t, 20, usage.CompletionTokens)
	assert.Empty(t, recorder.Body.String())
}

func TestOaiResponsesHandlerAllowsUsefulAndCompactResponses(t *testing.T) {
	tests := []struct {
		name string
		mode int
		body string
	}{
		{
			name: "tool call is useful",
			mode: relayconstant.RelayModeResponses,
			body: `{"id":"resp_tool","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`,
		},
		{
			name: "compact endpoint is exempt",
			mode: relayconstant.RelayModeResponsesCompact,
			body: reasoningOnlyResponsesBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newResponsesTestContext()
			enabled := true
			info := &relaycommon.RelayInfo{
				RelayMode: tt.mode,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelSetting: dto.ChannelSettings{ResponsesEmptyOutputGuard: &enabled},
				},
			}
			resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}

			_, apiErr := OaiResponsesHandler(ctx, info, resp)

			require.Nil(t, apiErr)
			assert.JSONEq(t, tt.body, recorder.Body.String())
		})
	}
}

func TestOaiResponsesStreamHandlerReplacesInvalidCompletedEvent(t *testing.T) {
	originalStreamingTimeout := appconstant.StreamingTimeout
	appconstant.StreamingTimeout = 30
	t.Cleanup(func() { appconstant.StreamingTimeout = originalStreamingTimeout })

	ctx, recorder := newResponsesTestContext()
	enabled := true
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-test",
			ChannelSetting:    dto.ChannelSettings{ResponsesEmptyOutputGuard: &enabled},
		},
	}
	streamBody := "data: {\"type\":\"response.completed\",\"sequence_number\":17,\"response\":" + reasoningOnlyResponsesBody + "}\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(streamBody)), Header: make(http.Header)}

	usage, apiErr := OaiResponsesStreamHandler(ctx, info, resp)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeUpstreamEmptyResponse, apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.True(t, types.IsSuppressResponseError(apiErr))
	assert.Equal(t, 20, usage.CompletionTokens)
	output := recorder.Body.String()
	assert.Contains(t, output, `"type":"response.failed"`)
	assert.Contains(t, output, `"sequence_number":17`)
	assert.Contains(t, output, `"code":"server_error"`)
	assert.Contains(t, output, `"output":[]`)
	assert.Contains(t, output, `"tools":[]`)
	assert.Contains(t, output, `"tool_choice":"auto"`)
	assert.NotContains(t, output, `"type":"response.completed"`)
	assert.NotContains(t, output, "must-not-leak")
}

func newResponsesTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return ctx, recorder
}
