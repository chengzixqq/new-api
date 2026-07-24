package openai

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type notifyingResponseWriter struct {
	header http.Header
	mu     sync.Mutex
	body   bytes.Buffer
	wrote  chan struct{}
	once   sync.Once
}

func newNotifyingResponseWriter() *notifyingResponseWriter {
	return &notifyingResponseWriter{
		header: make(http.Header),
		wrote:  make(chan struct{}),
	}
}

func (w *notifyingResponseWriter) Header() http.Header {
	return w.header
}

func (w *notifyingResponseWriter) WriteHeader(int) {}

func (w *notifyingResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.body.Write(data)
	if n > 0 {
		w.once.Do(func() { close(w.wrote) })
	}
	return n, err
}

func (w *notifyingResponseWriter) Flush() {}

func (w *notifyingResponseWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func newOpenAIStreamTestInfo(model string, includeUsage bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		StartTime:          time.Now(),
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: includeUsage,
		DisablePing:        true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: model,
		},
	}
}

func setOpenAIStreamTestTimeout(t *testing.T) {
	t.Helper()
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
}

func TestClassifyOpenAIStreamFrame(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		deferred bool
		terminal bool
		usage    bool
		payload  bool
	}{
		{
			name:    "content forwards immediately",
			data:    `{"choices":[{"delta":{"content":"hello"}}]}`,
			payload: true,
		},
		{
			name:    "reasoning forwards immediately",
			data:    `{"choices":[{"delta":{"reasoning_content":"think"}}]}`,
			payload: true,
		},
		{
			name:    "tool call forwards immediately",
			data:    `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{}"}}]}}]}`,
			payload: true,
		},
		{
			name:     "pure finish waits for finalization",
			data:     `{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			deferred: true,
			terminal: true,
		},
		{
			name:     "finish carrying content still forwards immediately",
			data:     `{"choices":[{"delta":{"content":"last"},"finish_reason":"stop"}]}`,
			terminal: true,
			payload:  true,
		},
		{
			name:     "usage waits for include usage decision",
			data:     `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2}}`,
			deferred: true,
			usage:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := classifyOpenAIStreamFrame(relayconstant.RelayModeChatCompletions, tt.data)
			assert.Equal(t, tt.terminal, frame.terminal)
			assert.Equal(t, tt.usage, frame.streamUsage != nil)
			assert.Equal(t, tt.payload, frame.hasPayload)
			assert.Equal(t, tt.deferred, shouldDeferOpenAIStreamFrame(frame))
		})
	}
}

func TestSendOpenAIStreamFrameRemovesUnrequestedMixedUsage(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := newOpenAIStreamTestInfo("gpt-4o-mini", false)
	data := `{"choices":[{"delta":{"content":"last"}}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`
	frame := classifyOpenAIStreamFrame(relayconstant.RelayModeChatCompletions, data)

	err := sendOpenAIStreamFrame(c, info, frame)

	require.NoError(t, err)
	assert.Contains(t, recorder.Body.String(), `"content":"last"`)
	assert.NotContains(t, recorder.Body.String(), `"usage"`)
}

func TestOaiStreamHandlerForwardsFirstContentWithoutNextChunk(t *testing.T) {
	setOpenAIStreamTestTimeout(t)

	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	downstream := newNotifyingResponseWriter()
	c, _ := gin.CreateTestContext(downstream)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := newOpenAIStreamTestInfo("gpt-4o-mini", false)
	resp := &http.Response{Body: reader, Header: make(http.Header)}

	done := make(chan struct{})
	go func() {
		_, _ = OaiStreamHandler(c, info, resp)
		close(done)
	}()

	firstChunk := `{"id":"chatcmpl-1","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"first-token"},"finish_reason":null}]}`
	_, err := fmt.Fprintf(writer, "data: %s\n\n", firstChunk)
	require.NoError(t, err)

	select {
	case <-downstream.wrote:
		assert.Contains(t, downstream.String(), "first-token")
	case <-time.After(2 * time.Second):
		t.Fatal("first content chunk was held waiting for a second upstream chunk")
	}

	finish := `{"id":"chatcmpl-1","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	_, err = fmt.Fprintf(writer, "data: %s\n\ndata: [DONE]\n\n", finish)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler did not finish")
	}
}

func TestOaiStreamHandlerTailFramesAndUsageSemantics(t *testing.T) {
	setOpenAIStreamTestTimeout(t)

	content := `{"id":"chatcmpl-2","created":2,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`
	finish := `{"id":"chatcmpl-2","created":2,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	usage := `{"id":"chatcmpl-2","created":2,"model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`
	body := strings.Join([]string{
		"data: " + content,
		"data: " + finish,
		"data: " + finish,
		"data: " + usage,
		"data: [DONE]",
		"",
	}, "\n\n")

	for _, includeUsage := range []bool{false, true} {
		t.Run(fmt.Sprintf("include_usage_%t", includeUsage), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := newOpenAIStreamTestInfo("gpt-4o-mini", includeUsage)
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

			gotUsage, relayErr := OaiStreamHandler(c, info, resp)

			require.Nil(t, relayErr)
			require.NotNil(t, gotUsage)
			assert.Equal(t, 3, gotUsage.PromptTokens)
			assert.Equal(t, 4, gotUsage.CompletionTokens)
			output := recorder.Body.String()
			assert.Equal(t, 1, strings.Count(output, `"finish_reason":"stop"`))
			assert.Equal(t, 1, strings.Count(output, "data: [DONE]"))
			if includeUsage {
				assert.Equal(t, 1, strings.Count(output, `"prompt_tokens":3`))
			} else {
				assert.NotContains(t, output, `"prompt_tokens":3`)
			}
		})
	}
}

func TestOaiStreamHandlerPreservesDeferredOrderAndSingleTermination(t *testing.T) {
	setOpenAIStreamTestTimeout(t)

	testCases := []struct {
		name          string
		frames        []string
		orderedBefore string
		orderedAfter  string
	}{
		{
			name: "deferred finish before later payload",
			frames: []string{
				`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`{"choices":[{"delta":{"content":"late-payload"},"finish_reason":null}]}`,
			},
			orderedBefore: `"finish_reason":"stop"`,
			orderedAfter:  "late-payload",
		},
		{
			name: "payload finish before duplicate pure finish",
			frames: []string{
				`{"choices":[{"delta":{"content":"last-payload"},"finish_reason":"stop"}]}`,
				`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			},
			orderedBefore: "last-payload",
			orderedAfter:  "data: [DONE]",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := make([]string, 0, len(testCase.frames)+2)
			for _, frame := range testCase.frames {
				lines = append(lines, "data: "+frame)
			}
			lines = append(lines, "data: [DONE]", "")

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := newOpenAIStreamTestInfo("gpt-4o-mini", false)
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Join(lines, "\n\n"))), Header: make(http.Header)}

			_, relayErr := OaiStreamHandler(c, info, resp)

			require.Nil(t, relayErr)
			output := recorder.Body.String()
			assert.Equal(t, 1, strings.Count(output, `"finish_reason":"stop"`))
			assert.Less(t, strings.Index(output, testCase.orderedBefore), strings.Index(output, testCase.orderedAfter))
		})
	}
}

func TestOaiStreamHandlerCombinedTerminalUsageHonorsIncludeUsage(t *testing.T) {
	setOpenAIStreamTestTimeout(t)
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
		"data: [DONE]",
		"",
	}, "\n\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := newOpenAIStreamTestInfo("gpt-4o-mini", false)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	gotUsage, relayErr := OaiStreamHandler(c, info, resp)

	require.Nil(t, relayErr)
	require.NotNil(t, gotUsage)
	assert.Equal(t, 3, gotUsage.PromptTokens)
	assert.Contains(t, recorder.Body.String(), `"finish_reason":"stop"`)
	assert.NotContains(t, recorder.Body.String(), `"usage"`)
}

func TestOaiStreamHandlerPreservesAudioUsageFromPenultimateFrame(t *testing.T) {
	setOpenAIStreamTestTimeout(t)

	content := `{"id":"chatcmpl-audio","created":3,"model":"gpt-4o-audio-preview","choices":[{"index":0,"delta":{"content":"audio"},"finish_reason":null}]}`
	usage := `{"id":"chatcmpl-audio","created":3,"model":"gpt-4o-audio-preview","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":6,"total_tokens":11}}`
	finish := `{"id":"chatcmpl-audio","created":3,"model":"gpt-4o-audio-preview","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	body := strings.Join([]string{
		"data: " + content,
		"data: " + usage,
		"data: " + finish,
		"data: [DONE]",
		"",
	}, "\n\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := newOpenAIStreamTestInfo("gpt-4o-audio-preview", false)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	gotUsage, relayErr := OaiStreamHandler(c, info, resp)

	require.Nil(t, relayErr)
	require.NotNil(t, gotUsage)
	assert.Equal(t, 5, gotUsage.PromptTokens)
	assert.Equal(t, 6, gotUsage.CompletionTokens)
	assert.NotContains(t, recorder.Body.String(), `"prompt_tokens":5`)
}

func TestOaiStreamHandlerStripsUsageFromCombinedTerminalFrame(t *testing.T) {
	setOpenAIStreamTestTimeout(t)

	content := `{"id":"chatcmpl-combined","created":4,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`
	terminalWithUsage := `{"id":"chatcmpl-combined","created":4,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":9,"total_tokens":17}}`
	body := strings.Join([]string{
		"data: " + content,
		"data: " + terminalWithUsage,
		"data: [DONE]",
		"",
	}, "\n\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := newOpenAIStreamTestInfo("gpt-4o-mini", false)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}

	gotUsage, relayErr := OaiStreamHandler(c, info, resp)

	require.Nil(t, relayErr)
	require.NotNil(t, gotUsage)
	assert.Equal(t, 8, gotUsage.PromptTokens)
	assert.Equal(t, 9, gotUsage.CompletionTokens)
	output := recorder.Body.String()
	assert.Equal(t, 1, strings.Count(output, `"finish_reason":"stop"`))
	assert.NotContains(t, output, `"prompt_tokens":8`)
}
