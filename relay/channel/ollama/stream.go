package ollama

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type ollamaChatStreamChunk struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	// chat
	Message *struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		Thinking  json.RawMessage  `json:"thinking"`
		ToolCalls []OllamaToolCall `json:"tool_calls"`
	} `json:"message"`
	// generate
	Response           string `json:"response"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	TotalDuration      int64  `json:"total_duration"`
	LoadDuration       int64  `json:"load_duration"`
	PromptEvalCount    int    `json:"prompt_eval_count"`
	EvalCount          int    `json:"eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalDuration       int64  `json:"eval_duration"`
}

func ollamaToolCallsToOpenAI(toolCalls []OllamaToolCall, startIndex int, includeIndex bool) ([]dto.ToolCallResponse, int) {
	if len(toolCalls) == 0 {
		return nil, startIndex
	}
	result := make([]dto.ToolCallResponse, 0, len(toolCalls))
	for _, tc := range toolCalls {
		var argBytes []byte
		var err error
		if tc.Function.Arguments == nil {
			argBytes = []byte("{}")
		} else {
			argBytes, err = common.Marshal(tc.Function.Arguments)
			if err != nil || len(argBytes) == 0 {
				argBytes = []byte("{}")
			}
		}
		tr := dto.ToolCallResponse{
			ID:   fmt.Sprintf("call_%d", startIndex),
			Type: "function",
			Function: dto.FunctionResponse{
				Name:      tc.Function.Name,
				Arguments: string(argBytes),
			},
		}
		if includeIndex {
			tr.SetIndex(startIndex)
		}
		startIndex++
		result = append(result, tr)
	}
	return result, startIndex
}

func toUnix(ts string) int64 {
	if ts == "" {
		return time.Now().Unix()
	}
	// try time.RFC3339 or with nanoseconds
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t2, err2 := time.Parse(time.RFC3339, ts)
		if err2 == nil {
			return t2.Unix()
		}
		return time.Now().Unix()
	}
	return t.Unix()
}

func writeOllamaStreamData(c *gin.Context, info *relaycommon.RelayInfo, payload any) error {
	data, err := common.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Ollama stream response: %w", err)
	}
	before := c.Writer.Size()
	err = helper.StringData(c, string(data))
	if c.Writer.Size() > before {
		info.MarkStreamOutputStarted()
		info.SetFirstFlushTime()
	}
	return err
}

func finishInterruptedOllamaStream(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	usage *dto.Usage,
	model string,
	observedOutput string,
	cause error,
) (*dto.Usage, *types.NewAPIError) {
	if cause == nil {
		cause = io.ErrUnexpectedEOF
	}
	info.StreamStatus.RecordError(cause.Error())
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, cause)
	logger.LogError(c, "ollama stream ended before done=true: "+cause.Error())

	if !info.StreamOutputStarted() {
		return nil, types.NewOpenAIError(cause, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}

	promptTokens := info.GetEstimatePromptTokens()
	if promptTokens < 0 {
		promptTokens = 0
	}
	completionTokens := service.CountTextToken(observedOutput, model)
	if completionTokens < 1 {
		completionTokens = 1
	}
	usage.PromptTokens = promptTokens
	usage.CompletionTokens = completionTokens
	usage.TotalTokens = promptTokens + completionTokens

	errorPayload := map[string]any{
		"error": map[string]any{
			"message": "upstream Ollama stream ended before done=true",
			"type":    "upstream_error",
			"code":    "ollama_premature_eof",
		},
	}
	if err := writeOllamaStreamData(c, info, errorPayload); err != nil {
		logger.LogError(c, "failed to write Ollama premature EOF error: "+err.Error())
	}
	helper.Done(c)
	return usage, nil
}

func ollamaStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("empty response"), types.ErrorCodeBadResponse, http.StatusBadRequest)
	}
	defer service.CloseResponseBodyGracefully(resp)

	info.StreamStatus = relaycommon.NewStreamStatus()
	info.ResetStreamOutputStarted()
	scanner := helper.NewStreamScanner(resp.Body, info)
	usage := &dto.Usage{}
	var model = info.UpstreamModelName
	var responseId = common.GetUUID()
	var created = time.Now().Unix()
	var toolCallIndex int
	var observedOutput strings.Builder
	started := false
	done := false

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var chunk ollamaChatStreamChunk
		if err := common.Unmarshal([]byte(line), &chunk); err != nil {
			decodeErr := fmt.Errorf("decode Ollama stream chunk: %w", err)
			return finishInterruptedOllamaStream(c, info, usage, model, observedOutput.String(), decodeErr)
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		created = toUnix(chunk.CreatedAt)

		if !chunk.Done {
			info.ReceivedResponseCount++
			// delta content
			var content string
			if chunk.Message != nil {
				content = chunk.Message.Content
			} else {
				content = chunk.Response
			}
			delta := dto.ChatCompletionsStreamResponse{
				Id:      responseId,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   model,
				Choices: []dto.ChatCompletionsStreamResponseChoice{{
					Index: 0,
					Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"},
				}},
			}
			if content != "" {
				delta.Choices[0].Delta.SetContentString(content)
				observedOutput.WriteString(content)
			}
			if chunk.Message != nil && len(chunk.Message.Thinking) > 0 {
				raw := strings.TrimSpace(string(chunk.Message.Thinking))
				if raw != "" && raw != "null" {
					// Unmarshal the JSON string to get the actual content without quotes
					var thinkingContent string
					if err := common.Unmarshal(chunk.Message.Thinking, &thinkingContent); err == nil {
						delta.Choices[0].Delta.SetReasoningContent(thinkingContent)
						observedOutput.WriteString(thinkingContent)
					} else {
						// Fallback to raw string if it's not a JSON string
						delta.Choices[0].Delta.SetReasoningContent(raw)
						observedOutput.WriteString(raw)
					}
				}
			}
			// tool calls
			if chunk.Message != nil && len(chunk.Message.ToolCalls) > 0 {
				delta.Choices[0].Delta.ToolCalls, toolCallIndex = ollamaToolCallsToOpenAI(chunk.Message.ToolCalls, toolCallIndex, true)
				if toolCallData, err := common.Marshal(chunk.Message.ToolCalls); err == nil {
					observedOutput.Write(toolCallData)
				}
			}
			if content == "" && delta.Choices[0].Delta.ReasoningContent == nil && len(delta.Choices[0].Delta.ToolCalls) == 0 {
				continue
			}
			if !started {
				info.SetFirstResponseTime()
				helper.SetEventStreamHeaders(c)
				start := helper.GenerateStartEmptyResponse(responseId, created, model, nil)
				if err := writeOllamaStreamData(c, info, start); err != nil {
					return finishInterruptedOllamaStream(c, info, usage, model, observedOutput.String(), err)
				}
				started = true
			}
			if err := writeOllamaStreamData(c, info, delta); err != nil {
				return finishInterruptedOllamaStream(c, info, usage, model, observedOutput.String(), err)
			}
			continue
		}
		if !started {
			info.SetFirstResponseTime()
			helper.SetEventStreamHeaders(c)
			start := helper.GenerateStartEmptyResponse(responseId, created, model, nil)
			if err := writeOllamaStreamData(c, info, start); err != nil {
				return finishInterruptedOllamaStream(c, info, usage, model, observedOutput.String(), err)
			}
			started = true
		}
		// done frame
		// finalize once and break loop
		usage.PromptTokens = chunk.PromptEvalCount
		usage.CompletionTokens = chunk.EvalCount
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		finishReason := chunk.DoneReason
		if finishReason == "" {
			finishReason = "stop"
		}
		if toolCallIndex > 0 {
			finishReason = constant.FinishReasonToolCalls
		}
		// emit stop delta
		if stop := helper.GenerateStopResponse(responseId, created, model, finishReason); stop != nil {
			if err := writeOllamaStreamData(c, info, stop); err != nil {
				return finishInterruptedOllamaStream(c, info, usage, model, observedOutput.String(), err)
			}
		}
		// emit usage frame
		if final := helper.GenerateFinalUsageResponse(responseId, created, model, *usage); final != nil {
			if err := writeOllamaStreamData(c, info, final); err != nil {
				return finishInterruptedOllamaStream(c, info, usage, model, observedOutput.String(), err)
			}
		}
		// send [DONE]
		helper.Done(c)
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
		done = true
		break
	}
	if done {
		return usage, nil
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return finishInterruptedOllamaStream(c, info, usage, model, observedOutput.String(), err)
	}
	return finishInterruptedOllamaStream(c, info, usage, model, observedOutput.String(), io.ErrUnexpectedEOF)
}

// non-stream handler for chat/generate
func ollamaChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("empty response"), types.ErrorCodeBadResponse, http.StatusBadRequest)
	}
	defer service.CloseResponseBodyGracefully(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	raw := string(body)
	if common.DebugEnabled {
		println("ollama non-stream raw resp:", raw)
	}

	var chunks []ollamaChatStreamChunk
	var single ollamaChatStreamChunk
	if err := common.Unmarshal(body, &single); err == nil {
		chunks = append(chunks, single)
	} else {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var chunk ollamaChatStreamChunk
			if err := common.Unmarshal([]byte(line), &chunk); err != nil {
				return nil, types.NewOpenAIError(fmt.Errorf("decode Ollama response chunk: %w", err), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
			}
			chunks = append(chunks, chunk)
		}
	}
	if len(chunks) == 0 {
		return nil, types.NewOpenAIError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}

	var (
		aggContent       strings.Builder
		reasoningBuilder strings.Builder
		lastChunk        ollamaChatStreamChunk
		toolCallIndex    int
		toolCalls        []dto.ToolCallResponse
	)
	for index, ck := range chunks {
		if lastChunk.Done {
			return nil, types.NewOpenAIError(fmt.Errorf("Ollama response contains data after done=true at chunk %d", index), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		lastChunk = ck
		if ck.Message != nil && len(ck.Message.Thinking) > 0 {
			raw := strings.TrimSpace(string(ck.Message.Thinking))
			if raw != "" && raw != "null" {
				// Unmarshal the JSON string to get the actual content without quotes
				var thinkingContent string
				if err := common.Unmarshal(ck.Message.Thinking, &thinkingContent); err == nil {
					reasoningBuilder.WriteString(thinkingContent)
				} else {
					// Fallback to raw string if it's not a JSON string
					reasoningBuilder.WriteString(raw)
				}
			}
		}
		if ck.Message != nil && ck.Message.Content != "" {
			aggContent.WriteString(ck.Message.Content)
		} else if ck.Response != "" {
			aggContent.WriteString(ck.Response)
		}
		if ck.Message != nil && len(ck.Message.ToolCalls) > 0 {
			var converted []dto.ToolCallResponse
			converted, toolCallIndex = ollamaToolCallsToOpenAI(ck.Message.ToolCalls, toolCallIndex, false)
			toolCalls = append(toolCalls, converted...)
		}
	}
	if !lastChunk.Done {
		return nil, types.NewOpenAIError(fmt.Errorf("Ollama response ended before done=true: %w", io.ErrUnexpectedEOF), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}

	model := lastChunk.Model
	if model == "" {
		model = info.UpstreamModelName
	}
	created := toUnix(lastChunk.CreatedAt)
	usage := &dto.Usage{PromptTokens: lastChunk.PromptEvalCount, CompletionTokens: lastChunk.EvalCount, TotalTokens: lastChunk.PromptEvalCount + lastChunk.EvalCount}
	content := aggContent.String()
	finishReason := lastChunk.DoneReason
	if finishReason == "" {
		finishReason = "stop"
	}
	if len(toolCalls) > 0 {
		finishReason = constant.FinishReasonToolCalls
	}

	msg := dto.Message{Role: "assistant", Content: contentPtr(content)}
	if len(toolCalls) > 0 {
		if rawToolCalls, err := common.Marshal(toolCalls); err == nil {
			msg.ToolCalls = rawToolCalls
		}
	}
	if rc := reasoningBuilder.String(); rc != "" {
		msg.ReasoningContent = &rc
	}
	full := dto.OpenAITextResponse{
		Id:      common.GetUUID(),
		Model:   model,
		Object:  "chat.completion",
		Created: created,
		Choices: []dto.OpenAITextResponseChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
		Usage: *usage,
	}
	out, _ := common.Marshal(full)
	service.IOCopyBytesGracefully(c, resp, out)
	return usage, nil
}

func contentPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
