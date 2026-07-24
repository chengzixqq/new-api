package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const upstreamEmptyResponseMessage = "upstream completed the response without usable output"

func shouldGuardResponsesEmptyOutput(info *relaycommon.RelayInfo) bool {
	if info == nil || info.RelayMode == relayconstant.RelayModeResponsesCompact {
		return false
	}
	if info.ChannelMeta == nil {
		return model_setting.ResponsesEmptyOutputGuardEnabled(nil)
	}
	return model_setting.ResponsesEmptyOutputGuardEnabled(info.ChannelSetting.ResponsesEmptyOutputGuard)
}

func newUpstreamEmptyResponseError(suppressResponse bool) *types.NewAPIError {
	options := []types.NewAPIErrorOptions{types.ErrOptionWithSkipRetry()}
	if suppressResponse {
		options = append(options, types.ErrOptionWithSuppressResponse())
	}
	return types.NewOpenAIError(
		fmt.Errorf("%s", upstreamEmptyResponseMessage),
		types.ErrorCodeUpstreamEmptyResponse,
		http.StatusFailedDependency,
		options...,
	)
}

func applyResponsesUsage(usage *dto.Usage, responseUsage *dto.Usage) {
	if usage == nil || responseUsage == nil {
		return
	}
	usage.PromptTokens = responseUsage.InputTokens
	usage.CompletionTokens = responseUsage.OutputTokens
	usage.TotalTokens = responseUsage.TotalTokens
	if responseUsage.InputTokensDetails != nil {
		usage.PromptTokensDetails.CachedTokens = responseUsage.InputTokensDetails.CachedTokens
		usage.PromptTokensDetails.CacheWriteTokens = responseUsage.InputTokensDetails.CacheWriteTokens
	}
}

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	if err = common.Unmarshal(responseBody, &responsesResponse); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	usage := &dto.Usage{}
	applyResponsesUsage(usage, responsesResponse.Usage)
	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
	}
	if shouldGuardResponsesEmptyOutput(info) && responsesResponse.IsCompletedWithoutUsableOutput() {
		logger.LogWarn(c, fmt.Sprintf("upstream Responses output guard rejected response: response_id=%s, model=%s, status=%s, output_items=%d, output_tokens=%d",
			responsesResponse.ID, responsesResponse.Model, responsesResponse.StatusString(), len(responsesResponse.Output), usage.CompletionTokens))
		return usage, newUpstreamEmptyResponseError(false)
	}

	// Delay the downstream write until the completed response passes semantic validation.
	service.IOCopyBytesGracefully(c, resp, responseBody)
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return usage, nil
	}
	for _, tool := range responsesResponse.Tools {
		builtInToolInfo, ok := info.ResponsesUsageInfo.BuiltInTools[common.Interface2String(tool["type"])]
		if !ok || builtInToolInfo == nil {
			logger.LogError(c, fmt.Sprintf("BuiltInTools not found for tool type: %v", tool["type"]))
			continue
		}
		builtInToolInfo.CallCount++
	}
	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}
	defer service.CloseResponseBodyGracefully(resp)

	usage := &dto.Usage{}
	var responseTextBuilder strings.Builder
	guardEmptyOutput := shouldGuardResponsesEmptyOutput(info)
	usableOutputObserved := false
	var emptyResponseError *types.NewAPIError

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}

		switch streamResponse.Type {
		case "response.completed":
			if streamResponse.Response != nil {
				applyResponsesUsage(usage, streamResponse.Response.Usage)
				if streamResponse.Response.HasImageGenerationCall() {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", streamResponse.Response.GetQuality())
					c.Set("image_generation_call_size", streamResponse.Response.GetSize())
				}
				if guardEmptyOutput && streamResponse.Response.IsCompletedWithoutUsableOutput() && !usableOutputObserved {
					emptyResponseError = newUpstreamEmptyResponseError(true)
					logger.LogWarn(c, fmt.Sprintf("upstream Responses output guard rejected stream: response_id=%s, model=%s, status=%s, output_items=%d, output_tokens=%d",
						streamResponse.Response.ID, streamResponse.Response.Model, streamResponse.Response.StatusString(), len(streamResponse.Response.Output), usage.CompletionTokens))
					sequenceNumber := streamResponse.SequenceNumber
					if sequenceNumber == nil {
						sequenceNumber = common.GetPointer(int64(0))
					}
					failedResponse := *streamResponse.Response
					failedResponse.Status = json.RawMessage(`"failed"`)
					failedResponse.Output = []dto.ResponsesOutput{}
					if failedResponse.Tools == nil {
						failedResponse.Tools = []map[string]any{}
					}
					if len(failedResponse.ToolChoice) == 0 || string(failedResponse.ToolChoice) == "null" {
						failedResponse.ToolChoice = json.RawMessage(`"auto"`)
					}
					failedResponse.Error = map[string]any{
						"code":    "server_error",
						"message": upstreamEmptyResponseMessage,
					}
					failedEvent := dto.ResponsesStreamResponse{
						Type:           "response.failed",
						SequenceNumber: sequenceNumber,
						Response:       &failedResponse,
					}
					failedData, err := common.Marshal(failedEvent)
					if err != nil {
						sr.Stop(err)
						return
					}
					sendResponsesStreamData(c, failedEvent, string(failedData))
					sr.Stop(emptyResponseError)
					return
				}
			}
		case "response.output_text.delta":
			responseTextBuilder.WriteString(streamResponse.Delta)
			if strings.TrimSpace(streamResponse.Delta) != "" {
				usableOutputObserved = true
			}
		case "response.refusal.delta":
			if strings.TrimSpace(streamResponse.Delta) != "" {
				usableOutputObserved = true
			}
		case dto.ResponsesOutputTypeItemAdded:
			if streamResponse.Item != nil && streamResponse.Item.HasUsableOutput() {
				usableOutputObserved = true
			}
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				if streamResponse.Item.HasUsableOutput() {
					usableOutputObserved = true
				}
				if streamResponse.Item.Type == dto.BuildInCallWebSearchCall && info != nil && info.ResponsesUsageInfo != nil && info.ResponsesUsageInfo.BuiltInTools != nil {
					if webSearchTool, exists := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists && webSearchTool != nil {
						webSearchTool.CallCount++
					}
				}
			}
		}
		sendResponsesStreamData(c, streamResponse, data)
	})

	if usage.CompletionTokens == 0 {
		if responseText := responseTextBuilder.String(); responseText != "" {
			usage.CompletionTokens = service.CountTextToken(responseText, info.UpstreamModelName)
		}
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage, emptyResponseError
}
