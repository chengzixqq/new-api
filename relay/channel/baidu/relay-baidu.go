package baidu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

// https://cloud.baidu.com/doc/WENXINWORKSHOP/s/flfmc9do2

var (
	baiduTokenStore         sync.Map
	baiduTokenRefreshing    sync.Map
	baiduTokenRefreshGroup  singleflight.Group
	baiduAccessTokenFetcher = fetchBaiduAccessToken
)

const (
	baiduTokenRequestTimeout    = 30 * time.Second
	baiduTokenResponseBodyLimit = 1 << 20
)

func requestOpenAI2Baidu(request dto.GeneralOpenAIRequest) *BaiduChatRequest {
	baiduRequest := BaiduChatRequest{
		Temperature:    request.Temperature,
		TopP:           lo.FromPtrOr(request.TopP, 0),
		PenaltyScore:   lo.FromPtrOr(request.FrequencyPenalty, 0),
		Stream:         lo.FromPtrOr(request.Stream, false),
		DisableSearch:  false,
		EnableCitation: false,
		UserId:         request.User,
	}
	if request.GetMaxTokens() != 0 {
		maxTokens := int(request.GetMaxTokens())
		if request.GetMaxTokens() == 1 {
			maxTokens = 2
		}
		baiduRequest.MaxOutputTokens = &maxTokens
	}
	for _, message := range request.Messages {
		if message.Role == "system" {
			baiduRequest.System = message.StringContent()
		} else {
			baiduRequest.Messages = append(baiduRequest.Messages, BaiduMessage{
				Role:    message.Role,
				Content: message.StringContent(),
			})
		}
	}
	return &baiduRequest
}

func responseBaidu2OpenAI(response *BaiduChatResponse) *dto.OpenAITextResponse {
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: response.Result,
		},
		FinishReason: "stop",
	}
	fullTextResponse := dto.OpenAITextResponse{
		Id:      response.Id,
		Object:  "chat.completion",
		Created: response.Created,
		Choices: []dto.OpenAITextResponseChoice{choice},
		Usage:   response.Usage,
	}
	return &fullTextResponse
}

func streamResponseBaidu2OpenAI(baiduResponse *BaiduChatStreamResponse) *dto.ChatCompletionsStreamResponse {
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(baiduResponse.Result)
	if baiduResponse.IsEnd {
		choice.FinishReason = &constant.FinishReasonStop
	}
	response := dto.ChatCompletionsStreamResponse{
		Id:      baiduResponse.Id,
		Object:  "chat.completion.chunk",
		Created: baiduResponse.Created,
		Model:   "ernie-bot",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response
}

func embeddingRequestOpenAI2Baidu(request dto.EmbeddingRequest) *BaiduEmbeddingRequest {
	return &BaiduEmbeddingRequest{
		Input: request.ParseInput(),
	}
}

func embeddingResponseBaidu2OpenAI(response *BaiduEmbeddingResponse) *dto.OpenAIEmbeddingResponse {
	openAIEmbeddingResponse := dto.OpenAIEmbeddingResponse{
		Object: "list",
		Data:   make([]dto.OpenAIEmbeddingResponseItem, 0, len(response.Data)),
		Model:  "baidu-embedding",
		Usage:  response.Usage,
	}
	for _, item := range response.Data {
		openAIEmbeddingResponse.Data = append(openAIEmbeddingResponse.Data, dto.OpenAIEmbeddingResponseItem{
			Object:    item.Object,
			Index:     item.Index,
			Embedding: item.Embedding,
		})
	}
	return &openAIEmbeddingResponse
}

func baiduStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	usage := &dto.Usage{}
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var baiduResponse BaiduChatStreamResponse
		if err := common.Unmarshal([]byte(data), &baiduResponse); err != nil {
			common.SysLog("error unmarshalling stream response: " + err.Error())
			sr.Error(err)
			return
		}
		if baiduResponse.Usage.TotalTokens != 0 {
			usage.TotalTokens = baiduResponse.Usage.TotalTokens
			usage.PromptTokens = baiduResponse.Usage.PromptTokens
			usage.CompletionTokens = baiduResponse.Usage.TotalTokens - baiduResponse.Usage.PromptTokens
		}
		response := streamResponseBaidu2OpenAI(&baiduResponse)
		if err := helper.ObjectData(c, response); err != nil {
			common.SysLog("error sending stream response: " + err.Error())
			sr.Error(err)
		}
	})
	service.CloseResponseBodyGracefully(resp)
	return nil, usage
}

func baiduHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	var baiduResponse BaiduChatResponse
	responseBody, err := service.ReadResponseBodyLimited(resp.Body, baiduTokenResponseBodyLimit)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	service.CloseResponseBodyGracefully(resp)
	err = common.Unmarshal(responseBody, &baiduResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	if baiduResponse.ErrorMsg != "" {
		return types.NewError(fmt.Errorf("%s", baiduResponse.ErrorMsg), types.ErrorCodeBadResponseBody), nil
	}
	fullTextResponse := responseBaidu2OpenAI(&baiduResponse)
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return nil, &fullTextResponse.Usage
}

func baiduEmbeddingHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	var baiduResponse BaiduEmbeddingResponse
	responseBody, err := service.ReadResponseBodyLimited(resp.Body, baiduTokenResponseBodyLimit)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	service.CloseResponseBodyGracefully(resp)
	err = common.Unmarshal(responseBody, &baiduResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	if baiduResponse.ErrorMsg != "" {
		return types.NewError(fmt.Errorf("%s", baiduResponse.ErrorMsg), types.ErrorCodeBadResponseBody), nil
	}
	fullTextResponse := embeddingResponseBaidu2OpenAI(&baiduResponse)
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return nil, &fullTextResponse.Usage
}

func getBaiduAccessToken(ctx context.Context, cacheKey, apiKey, proxy string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	if value, ok := baiduTokenStore.Load(cacheKey); ok {
		if token, valid := value.(BaiduAccessToken); valid && token.AccessToken != "" && now.Before(token.ExpiresAt) {
			if !token.RefreshAt.IsZero() && !now.Before(token.RefreshAt) {
				triggerBaiduTokenRefresh(cacheKey, apiKey, proxy)
			}
			return token.AccessToken, nil
		}
	}
	token, err := refreshBaiduAccessToken(ctx, cacheKey, apiKey, proxy, false)
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func triggerBaiduTokenRefresh(cacheKey, apiKey, proxy string) {
	if _, loaded := baiduTokenRefreshing.LoadOrStore(cacheKey, struct{}{}); loaded {
		return
	}
	go func() {
		defer baiduTokenRefreshing.Delete(cacheKey)
		_, _ = refreshBaiduAccessToken(context.Background(), cacheKey, apiKey, proxy, true)
	}()
}

func refreshBaiduAccessToken(ctx context.Context, cacheKey, apiKey, proxy string, force bool) (*BaiduAccessToken, error) {
	value, err, _ := baiduTokenRefreshGroup.Do(cacheKey, func() (any, error) {
		if !force {
			if cached, ok := baiduTokenStore.Load(cacheKey); ok {
				if token, valid := cached.(BaiduAccessToken); valid && token.AccessToken != "" && time.Now().Before(token.ExpiresAt) {
					return &token, nil
				}
			}
		}
		token, fetchErr := baiduAccessTokenFetcher(ctx, apiKey, proxy)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if token == nil || token.AccessToken == "" {
			return nil, errors.New("baidu token endpoint returned an empty token")
		}
		baiduTokenStore.Store(cacheKey, *token)
		return token, nil
	})
	if err != nil {
		return nil, err
	}
	token, ok := value.(*BaiduAccessToken)
	if !ok || token == nil {
		return nil, errors.New("baidu token refresh returned an invalid result")
	}
	return token, nil
}

func fetchBaiduAccessToken(parent context.Context, apiKey, proxy string) (*BaiduAccessToken, error) {
	parts := strings.Split(apiKey, "|")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return nil, errors.New("invalid baidu apikey")
	}
	query := url.Values{
		"grant_type":    []string{"client_credentials"},
		"client_id":     []string{strings.TrimSpace(parts[0])},
		"client_secret": []string{strings.TrimSpace(parts[1])},
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, baiduTokenRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://aip.baidubce.com/oauth/2.0/token?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := service.ReadResponseBodyLimited(res.Body, baiduTokenResponseBodyLimit)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("baidu token endpoint returned status %d", res.StatusCode)
	}
	var accessToken BaiduAccessToken
	if err := common.Unmarshal(body, &accessToken); err != nil {
		return nil, err
	}
	if accessToken.Error != "" {
		return nil, errors.New(accessToken.Error + ": " + accessToken.ErrorDescription)
	}
	if accessToken.AccessToken == "" || accessToken.ExpiresIn <= 0 {
		return nil, errors.New("baidu token endpoint returned invalid token metadata")
	}
	lifetime := time.Duration(accessToken.ExpiresIn) * time.Second
	refreshWindow := time.Hour
	if tenth := lifetime / 10; tenth < refreshWindow {
		refreshWindow = tenth
	}
	accessToken.ExpiresAt = time.Now().Add(lifetime)
	accessToken.RefreshAt = accessToken.ExpiresAt.Add(-refreshWindow)
	return &accessToken, nil
}
