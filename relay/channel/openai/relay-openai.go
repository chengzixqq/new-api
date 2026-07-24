package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openrouter"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func sendStreamData(c *gin.Context, info *relaycommon.RelayInfo, data string, forceFormat bool, thinkToContent bool) error {
	if data == "" {
		return nil
	}

	if !forceFormat && !thinkToContent {
		return helper.StringData(c, data)
	}

	var lastStreamResponse dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(data, &lastStreamResponse); err != nil {
		return err
	}

	if !thinkToContent {
		return helper.ObjectData(c, lastStreamResponse)
	}

	hasThinkingContent := false
	hasContent := false
	var thinkingContent strings.Builder
	for _, choice := range lastStreamResponse.Choices {
		if len(choice.Delta.GetReasoningContent()) > 0 {
			hasThinkingContent = true
			thinkingContent.WriteString(choice.Delta.GetReasoningContent())
		}
		if len(choice.Delta.GetContentString()) > 0 {
			hasContent = true
		}
	}

	// Handle think to content conversion
	if info.ThinkingContentInfo.IsFirstThinkingContent {
		if hasThinkingContent {
			response := lastStreamResponse.Copy()
			for i := range response.Choices {
				// send `think` tag with thinking content
				response.Choices[i].Delta.SetContentString("<think>\n" + thinkingContent.String())
				response.Choices[i].Delta.ReasoningContent = nil
				response.Choices[i].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.IsFirstThinkingContent = false
			info.ThinkingContentInfo.HasSentThinkingContent = true
			return helper.ObjectData(c, response)
		}
	}

	if lastStreamResponse.Choices == nil || len(lastStreamResponse.Choices) == 0 {
		return helper.ObjectData(c, lastStreamResponse)
	}

	// Process each choice
	for i, choice := range lastStreamResponse.Choices {
		// Handle transition from thinking to content
		// only send `</think>` tag when previous thinking content has been sent
		if hasContent && !info.ThinkingContentInfo.SendLastThinkingContent && info.ThinkingContentInfo.HasSentThinkingContent {
			response := lastStreamResponse.Copy()
			for j := range response.Choices {
				response.Choices[j].Delta.SetContentString("\n</think>\n")
				response.Choices[j].Delta.ReasoningContent = nil
				response.Choices[j].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.SendLastThinkingContent = true
			helper.ObjectData(c, response)
		}

		// Convert reasoning content to regular content if any
		if len(choice.Delta.GetReasoningContent()) > 0 {
			lastStreamResponse.Choices[i].Delta.SetContentString(choice.Delta.GetReasoningContent())
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		} else if !hasThinkingContent && !hasContent {
			// flush thinking content
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		}
	}

	return helper.ObjectData(c, lastStreamResponse)
}

type openAIStreamFrame struct {
	data         string
	terminal     bool
	streamUsage  *dto.Usage
	hasPayload   bool
	omitTerminal bool
}

// classifyOpenAIStreamFrame identifies frames whose downstream representation
// depends on end-of-stream state. Content, reasoning, tool-call, and ordinary
// metadata frames can be forwarded immediately; pure usage and finish frames
// are kept until the tail is known so include_usage and format conversion keep
// their existing semantics.
func classifyOpenAIStreamFrame(relayMode int, data string) openAIStreamFrame {
	frame := openAIStreamFrame{data: data}

	switch relayMode {
	case relayconstant.RelayModeChatCompletions:
		var response dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &response); err != nil {
			return frame
		}
		frame.streamUsage = response.Usage
		for _, choice := range response.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				frame.terminal = true
			}
			if choice.Delta.GetContentString() != "" ||
				choice.Delta.GetReasoningContent() != "" ||
				len(choice.Delta.ToolCalls) > 0 {
				frame.hasPayload = true
			}
		}
	case relayconstant.RelayModeCompletions:
		var response dto.CompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &response); err != nil {
			return frame
		}
		for _, choice := range response.Choices {
			if choice.FinishReason != "" {
				frame.terminal = true
			}
			if choice.Text != "" {
				frame.hasPayload = true
			}
		}
	}

	return frame
}

func shouldDeferOpenAIStreamFrame(frame openAIStreamFrame) bool {
	return !frame.hasPayload && (frame.streamUsage != nil || frame.terminal)
}

func sendOpenAIStreamFrame(c *gin.Context, info *relaycommon.RelayInfo, frame openAIStreamFrame) error {
	data := frame.data
	if (frame.streamUsage != nil && !info.ShouldIncludeUsage) || frame.omitTerminal {
		var payload map[string]any
		if err := common.UnmarshalJsonStr(data, &payload); err != nil {
			return err
		}
		if frame.streamUsage != nil && !info.ShouldIncludeUsage {
			delete(payload, "usage")
		}
		if frame.omitTerminal {
			if choices, ok := payload["choices"].([]any); ok {
				for _, rawChoice := range choices {
					if choice, ok := rawChoice.(map[string]any); ok {
						delete(choice, "finish_reason")
					}
				}
			}
		}
		filtered, err := common.Marshal(payload)
		if err != nil {
			return err
		}
		data = string(filtered)
	}
	return HandleStreamFormat(c, info, data, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent)
}

func sendDeferredOpenAIStreamFrames(c *gin.Context, info *relaycommon.RelayInfo, frames []openAIStreamFrame) error {
	for _, frame := range frames {
		if frame.streamUsage != nil && !frame.terminal && !info.ShouldIncludeUsage {
			continue
		}
		if err := sendOpenAIStreamFrame(c, info, frame); err != nil {
			return err
		}
	}
	return nil
}

func OaiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	model := info.UpstreamModelName
	var responseId string
	var createAt int64 = 0
	var systemFingerprint string
	var containStreamUsage bool
	var responseTextBuilder strings.Builder
	var toolCount int
	var usage = &dto.Usage{}
	var lastStreamData string
	deferredFrames := make([]openAIStreamFrame, 0, 2)
	terminalSeen := false
	var secondLastStreamData string // 存储倒数第二个stream data，用于音频模型

	// 检查是否为音频模型
	isAudioModel := strings.Contains(strings.ToLower(model), "audio")

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if len(data) > 0 {
			// 对音频模型，保存倒数第二个stream data
			if isAudioModel && lastStreamData != "" {
				secondLastStreamData = lastStreamData
			}

			lastStreamData = data
			if err := processTokenData(info.RelayMode, data, &responseTextBuilder, &toolCount); err != nil {
				logger.LogError(c, "error processing stream token data: "+err.Error())
				sr.Error(err)
			}

			frame := classifyOpenAIStreamFrame(info.RelayMode, data)
			if service.ValidUsage(frame.streamUsage) {
				usage = frame.streamUsage
				containStreamUsage = true
			}
			if frame.terminal {
				if terminalSeen {
					if !frame.hasPayload {
						return
					}
					frame.omitTerminal = true
				} else {
					terminalSeen = true
				}
			}
			if shouldDeferOpenAIStreamFrame(frame) {
				deferredFrames = append(deferredFrames, frame)
				return
			}
			if len(deferredFrames) > 0 {
				if err := sendDeferredOpenAIStreamFrames(c, info, deferredFrames); err != nil {
					common.SysLog("error handling deferred stream format: " + err.Error())
					sr.Error(err)
				}
				deferredFrames = deferredFrames[:0]
			}

			if err := sendOpenAIStreamFrame(c, info, frame); err != nil {
				common.SysLog("error handling stream format: " + err.Error())
				sr.Error(err)
			}
		}
	})

	// 对音频模型，从倒数第二个stream data中提取usage信息
	if isAudioModel && secondLastStreamData != "" {
		var streamResp struct {
			Usage *dto.Usage `json:"usage"`
		}
		err := common.Unmarshal([]byte(secondLastStreamData), &streamResp)
		if err == nil && streamResp.Usage != nil && service.ValidUsage(streamResp.Usage) {
			usage = streamResp.Usage
			containStreamUsage = true

			if common.DebugEnabled {
				logger.LogDebug(c, "Audio model usage extracted from second last SSE: PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d, InputTokens=%d, OutputTokens=%d",
					usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens,
					usage.InputTokens, usage.OutputTokens)
			}
		}
	}

	// 处理最后的响应
	// Forward deferred tail frames in arrival order, leaving the final frame
	// for the format-aware finalizer. Pure usage frames stay hidden unless the
	// client requested include_usage.
	if len(deferredFrames) > 1 {
		if err := sendDeferredOpenAIStreamFrames(c, info, deferredFrames[:len(deferredFrames)-1]); err != nil {
			common.SysLog("error handling deferred stream format: " + err.Error())
		}
	}

	finalStreamData := lastStreamData
	shouldFinalizeLastFrame := false
	if len(deferredFrames) > 0 {
		finalStreamData = deferredFrames[len(deferredFrames)-1].data
		shouldFinalizeLastFrame = true
	}

	shouldSendLastResp := true
	if err := handleLastResponse(finalStreamData, &responseId, &createAt, &systemFingerprint, &model, &usage,
		&containStreamUsage, info, &shouldSendLastResp); err != nil {
		logger.LogError(c, fmt.Sprintf("error handling last response: %s, lastStreamData: [%s]", err.Error(), finalStreamData))
	}

	if shouldFinalizeLastFrame && info.RelayFormat == types.RelayFormatOpenAI {
		if shouldSendLastResp {
			_ = sendOpenAIStreamFrame(c, info, deferredFrames[len(deferredFrames)-1])
		}
	}

	if !containStreamUsage {
		usage = service.ResponseText2Usage(c, responseTextBuilder.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		usage.CompletionTokens += toolCount * 7
	}

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(finalStreamData))

	handleFinalResponse(c, info, finalStreamData, responseId, createAt, model, systemFingerprint, usage, containStreamUsage, shouldFinalizeLastFrame)

	return usage, nil
}

func OpenaiHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	var simpleResponse dto.OpenAITextResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	logger.LogDebug(c, "upstream response body: %s", responseBody)
	// Unmarshal to simpleResponse
	if info.ChannelType == constant.ChannelTypeOpenRouter && info.ChannelOtherSettings.IsOpenRouterEnterprise() {
		// 尝试解析为 openrouter enterprise
		var enterpriseResponse openrouter.OpenRouterEnterpriseResponse
		err = common.Unmarshal(responseBody, &enterpriseResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if enterpriseResponse.Success {
			responseBody = enterpriseResponse.Data
		} else {
			logger.LogError(c, fmt.Sprintf("openrouter enterprise response success=false, data: %s", enterpriseResponse.Data))
			return nil, types.NewOpenAIError(fmt.Errorf("openrouter response success=false"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}

	err = common.Unmarshal(responseBody, &simpleResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if oaiError := simpleResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	for _, choice := range simpleResponse.Choices {
		if choice.FinishReason == constant.FinishReasonContentFilter {
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "openai_finish_reason=content_filter")
			break
		}
	}

	forceFormat := false
	if info.ChannelSetting.ForceFormat {
		forceFormat = true
	}

	usageModified := false
	if simpleResponse.Usage.PromptTokens == 0 {
		completionTokens := simpleResponse.Usage.CompletionTokens
		if completionTokens == 0 {
			for _, choice := range simpleResponse.Choices {
				ctkm := service.CountTextToken(choice.Message.StringContent()+choice.Message.GetReasoningContent(), info.UpstreamModelName)
				completionTokens += ctkm
			}
		}
		simpleResponse.Usage = dto.Usage{
			PromptTokens:     info.GetEstimatePromptTokens(),
			CompletionTokens: completionTokens,
			TotalTokens:      info.GetEstimatePromptTokens() + completionTokens,
		}
		usageModified = true
	}

	applyUsagePostProcessing(info, &simpleResponse.Usage, responseBody)

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		if usageModified {
			var bodyMap map[string]interface{}
			err = common.Unmarshal(responseBody, &bodyMap)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
			bodyMap["usage"] = simpleResponse.Usage
			responseBody, _ = common.Marshal(bodyMap)
		}
		if forceFormat {
			responseBody, err = common.Marshal(simpleResponse)
			if err != nil {
				return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
			}
		} else {
			break
		}
	case types.RelayFormatClaude:
		convertResult, err := relayconvert.ConvertResponse(c, info, types.RelayFormatClaude, &simpleResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		claudeRespStr, err := common.Marshal(convertResult.Value)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = claudeRespStr
	case types.RelayFormatGemini:
		convertResult, err := relayconvert.ConvertResponse(c, info, types.RelayFormatGemini, &simpleResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		geminiRespStr, err := common.Marshal(convertResult.Value)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = geminiRespStr
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return &simpleResponse.Usage, nil
}
