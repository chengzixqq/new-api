package coze

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	rootcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const (
	cozeResponseBodyLimit = 1 << 20
	cozeMaxPollAttempts   = 300
	cozeDetailMaxAttempts = 3
	cozePollInterval      = time.Second
)

var errCozeTerminalStatus = errors.New("coze chat reached a terminal failure status")

type Adaptor struct{}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertAudioRequest(*gin.Context, *relaycommon.RelayInfo, dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertEmbeddingRequest(*gin.Context, *relaycommon.RelayInfo, dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(*gin.Context, *relaycommon.RelayInfo, dto.ImageRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, _ *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return convertCozeChatRequest(c, *request), nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertRerankRequest(*gin.Context, int, dto.RerankRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info.IsStream {
		return channel.DoApiRequest(a, c, info, requestBody)
	}
	resp, err := channel.DoApiRequest(a, c, info, requestBody)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("coze create chat returned an empty response")
	}
	respBody, readErr := service.ReadResponseBodyLimited(resp.Body, cozeResponseBodyLimit)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("coze create chat returned status %d: %s", resp.StatusCode, rootcommon.LocalLogPreview(string(respBody)))
	}
	var cozeResponse CozeChatResponse
	if err := rootcommon.Unmarshal(respBody, &cozeResponse); err != nil {
		return nil, err
	}
	if cozeResponse.Code != 0 {
		return nil, errors.New(cozeResponse.Msg)
	}
	c.Set("coze_conversation_id", cozeResponse.Data.ConversationId)
	c.Set("coze_chat_id", cozeResponse.Data.Id)

	if err := pollCozeChat(c.Request.Context(), cozeMaxPollAttempts, cozePollInterval, func() (error, bool) {
		return checkIfChatComplete(a, c, info)
	}); err != nil {
		return nil, err
	}
	return getChatDetail(a, c, info)
}

func pollCozeChat(ctx context.Context, maxAttempts int, interval time.Duration, check func() (error, bool)) error {
	if maxAttempts <= 0 || check == nil {
		return errors.New("invalid coze polling configuration")
	}
	var lastPollErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pollErr, complete := check()
		if pollErr != nil {
			if errors.Is(pollErr, errCozeTerminalStatus) {
				return pollErr
			}
			lastPollErr = pollErr
		} else if complete {
			return nil
		}
		if attempt < maxAttempts {
			if err := waitCozePoll(ctx, interval); err != nil {
				return err
			}
		}
	}
	if lastPollErr != nil {
		return fmt.Errorf("coze polling exhausted %d attempts: %w", maxAttempts, lastPollErr)
	}
	return fmt.Errorf("coze polling timed out after %d attempts", maxAttempts)
}

func waitCozePoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.IsStream {
		return cozeChatStreamHandler(c, info, resp)
	}
	return cozeChatHandler(c, info, resp)
}

func (a *Adaptor) GetChannelName() string { return ChannelName }

func (a *Adaptor) GetModelList() []string { return ModelList }

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/v3/chat", info.ChannelBaseUrl), nil
}

func (a *Adaptor) Init(*relaycommon.RelayInfo) {}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}
