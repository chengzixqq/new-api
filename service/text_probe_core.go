package service

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/samber/lo"
)

const (
	TextProbeProtocolOpenAIChat      = model.ModelHealthProtocolOpenAIChat
	TextProbeProtocolOpenAIResponses = model.ModelHealthProtocolOpenAIResponses
	TextProbeProtocolAnthropic       = model.ModelHealthProtocolAnthropicMessages
	TextProbeProtocolGemini          = model.ModelHealthProtocolGeminiContent
)

// TextProbeRequest is the shared semantic request used by channel tests and
// model-health probes. Protocol-specific code only adapts this specification;
// model, prompt, stream mode, and output bound stay consistent.
type TextProbeRequest struct {
	Model           string
	Prompt          string
	Stream          bool
	MaxOutputTokens uint
}

func NewTextProbeRequest(modelName, prompt string, stream bool, maxOutputTokens uint) (TextProbeRequest, error) {
	request := TextProbeRequest{
		Model: strings.TrimSpace(modelName), Prompt: strings.TrimSpace(prompt),
		Stream: stream, MaxOutputTokens: maxOutputTokens,
	}
	if request.Model == "" || request.Prompt == "" {
		return TextProbeRequest{}, errors.New("text probe model and prompt are required")
	}
	if request.MaxOutputTokens == 0 {
		return TextProbeRequest{}, errors.New("text probe output limit must be positive")
	}
	return request, nil
}

func (request TextProbeRequest) RelayRequest(protocol string) (dto.Request, error) {
	switch protocol {
	case TextProbeProtocolOpenAIResponses:
		input, err := common.Marshal([]map[string]string{{"role": "user", "content": request.Prompt}})
		if err != nil {
			return nil, err
		}
		responseRequest := &dto.OpenAIResponsesRequest{
			Model: request.Model, Input: json.RawMessage(input), Stream: lo.ToPtr(request.Stream),
			MaxOutputTokens: lo.ToPtr(request.MaxOutputTokens),
		}
		if request.Stream {
			responseRequest.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
		}
		return responseRequest, nil
	case TextProbeProtocolOpenAIChat, TextProbeProtocolAnthropic, TextProbeProtocolGemini:
		chatRequest := &dto.GeneralOpenAIRequest{
			Model: request.Model, Stream: lo.ToPtr(request.Stream),
			Messages:  []dto.Message{{Role: "user", Content: request.Prompt}},
			MaxTokens: lo.ToPtr(request.MaxOutputTokens),
		}
		if request.Stream {
			chatRequest.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
		}
		return chatRequest, nil
	default:
		return nil, errors.New("unsupported text probe protocol")
	}
}

func (request TextProbeRequest) DirectPayload(protocol string) (any, error) {
	switch protocol {
	case TextProbeProtocolOpenAIChat:
		return map[string]any{
			"model": request.Model, "stream": request.Stream,
			"max_tokens": request.MaxOutputTokens,
			"messages":   []map[string]string{{"role": "user", "content": request.Prompt}},
		}, nil
	case TextProbeProtocolOpenAIResponses:
		return map[string]any{
			"model": request.Model, "stream": request.Stream,
			"max_output_tokens": request.MaxOutputTokens, "input": request.Prompt,
		}, nil
	case TextProbeProtocolAnthropic:
		return map[string]any{
			"model": request.Model, "stream": request.Stream,
			"max_tokens": request.MaxOutputTokens,
			"messages":   []map[string]string{{"role": "user", "content": request.Prompt}},
		}, nil
	case TextProbeProtocolGemini:
		return map[string]any{
			"contents": []any{map[string]any{
				"role":  "user",
				"parts": []any{map[string]string{"text": request.Prompt}},
			}},
			"generationConfig": map[string]any{"maxOutputTokens": request.MaxOutputTokens},
		}, nil
	default:
		return nil, errors.New("unsupported text probe protocol")
	}
}

func ValidateTextProbeAnswer(answer, expected string) bool {
	answer = strings.TrimSpace(answer)
	expected = strings.TrimSpace(expected)
	return answer != "" && answer == expected
}
