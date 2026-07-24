package ollama

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaChatHandlerNonStreamToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "compact json per-line parse path",
			raw:  `{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Paris","days":0}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
		},
		{
			name: "pretty json fallback parse path",
			raw: `{
  "model": "llama3.1",
  "created_at": "2026-05-27T12:00:00Z",
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "function": {
          "name": "get_weather",
          "arguments": {
            "city": "Paris",
            "days": 0
          }
        }
      }
    ]
  },
  "done": true,
  "done_reason": "stop",
  "prompt_eval_count": 5,
  "eval_count": 7
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.raw)),
			}

			usage, apiErr := ollamaChatHandler(c, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
			}, resp)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 12, usage.TotalTokens)

			var out dto.OpenAITextResponse
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
			require.Len(t, out.Choices, 1)
			assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)

			var toolCalls []dto.ToolCallResponse
			require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &toolCalls))
			require.Len(t, toolCalls, 1)
			assert.NotEmpty(t, toolCalls[0].ID)
			assert.Equal(t, "function", toolCalls[0].Type)
			assert.Equal(t, "get_weather", toolCalls[0].Function.Name)
			assert.Nil(t, toolCalls[0].Index)

			var args map[string]any
			require.NoError(t, common.Unmarshal([]byte(toolCalls[0].Function.Arguments), &args))
			assert.Equal(t, "Paris", args["city"])
			assert.Equal(t, float64(0), args["days"])
		})
	}
}

func TestOllamaChatHandlerRejectsIncompleteResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "missing terminal done frame",
			raw:  `{"model":"llama3.1","message":{"role":"assistant","content":"partial"},"done":false}`,
		},
		{
			name: "malformed NDJSON chunk",
			raw:  "{\"model\":\"llama3.1\",\"response\":\"partial\",\"done\":false}\nnot-json\n{\"done\":true}",
		},
		{
			name: "data after terminal frame",
			raw:  "{\"model\":\"llama3.1\",\"done\":true}\n{\"response\":\"late\",\"done\":false}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.raw)),
			}

			usage, apiErr := ollamaChatHandler(c, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
			}, resp)

			require.NotNil(t, apiErr)
			assert.Nil(t, usage)
			assert.Empty(t, w.Body.String())
		})
	}
}

func TestOllamaStreamHandlerRejectsEOFBeforeOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tt := range []struct {
		name string
		raw  string
	}{
		{name: "empty body"},
		{name: "empty upstream delta", raw: "{\"model\":\"llama3.1\",\"message\":{\"role\":\"assistant\",\"content\":\"\"},\"done\":false}\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.raw)),
			}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.1"},
			}

			usage, apiErr := ollamaStreamHandler(c, info, resp)

			require.NotNil(t, apiErr)
			assert.Nil(t, usage)
			assert.Empty(t, w.Body.String())
			require.NotNil(t, info.StreamStatus)
			assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
			assert.False(t, info.StreamOutputStarted())
		})
	}
}

func TestOllamaStreamHandlerConservativelyBillsPrematureEOF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			"{\"model\":\"llama3.1\",\"message\":{\"role\":\"assistant\",\"content\":\"partial output\"},\"done\":false}\n",
		)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.1"},
	}
	info.SetEstimatePromptTokens(11)

	usage, apiErr := ollamaStreamHandler(c, info, resp)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 11, usage.PromptTokens)
	assert.Positive(t, usage.CompletionTokens)
	assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
	assert.Contains(t, w.Body.String(), "partial output")
	assert.Contains(t, w.Body.String(), "ollama_premature_eof")
	assert.Contains(t, w.Body.String(), "data: [DONE]")
	assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
	assert.True(t, info.StreamOutputStarted())
}

func TestOllamaStreamHandlerMalformedChunkAfterOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			"{\"model\":\"llama3.1\",\"response\":\"partial\",\"done\":false}\nnot-json\n",
		)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.1"},
	}

	usage, apiErr := ollamaStreamHandler(c, info, resp)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Positive(t, usage.CompletionTokens)
	assert.Contains(t, w.Body.String(), "ollama_premature_eof")
	assert.Contains(t, w.Body.String(), "data: [DONE]")
	assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
}

func TestOllamaStreamHandlerCompletesOnlyOnDoneFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			"{\"model\":\"llama3.1\",\"message\":{\"role\":\"assistant\",\"content\":\"complete\"},\"done\":false}\n" +
				"{\"model\":\"llama3.1\",\"done\":true,\"done_reason\":\"stop\",\"prompt_eval_count\":5,\"eval_count\":2}\n",
		)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "llama3.1"},
	}

	usage, apiErr := ollamaStreamHandler(c, info, resp)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.TotalTokens)
	assert.Contains(t, w.Body.String(), "complete")
	assert.NotContains(t, w.Body.String(), "ollama_premature_eof")
	assert.Contains(t, w.Body.String(), "data: [DONE]")
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
}
