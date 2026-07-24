package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldRetryStopsAfterDownstreamCancellation(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	ginContext.Request = httptest.NewRequestWithContext(requestContext, http.MethodPost, "/v1/chat/completions", nil)
	cancel()

	apiErr := types.NewErrorWithStatusCode(errors.New("upstream unavailable"), types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)
	require.False(t, shouldRetry(ginContext, apiErr, 3))
	require.ErrorIs(t, relayRequestContextError(ginContext), context.Canceled)
}

func TestShouldRetryHonorsChannelZeroOverride(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	channelErr := types.NewError(errors.New("channel unavailable"), types.ErrorCodeChannelNoAvailableKey)

	require.False(t, shouldRetry(ginContext, channelErr, 0))
	require.True(t, shouldRetry(ginContext, channelErr, 1))
}

func TestShouldRetryAlwaysStopsForUpstreamEmptyResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	apiErr := types.NewOpenAIError(
		errors.New("empty upstream response"),
		types.ErrorCodeUpstreamEmptyResponse,
		http.StatusFailedDependency,
		types.ErrOptionWithSkipRetry(),
	)

	require.False(t, shouldRetry(ginContext, apiErr, 10))
}

func TestSelectedChannelMaxRetriesUsesInitialContextSetting(t *testing.T) {
	original := common.RetryTimes
	common.RetryTimes = 3
	t.Cleanup(func() { common.RetryTimes = original })

	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	zero := 0
	common.SetContextKey(ginContext, constant.ContextKeyChannelSetting, dto.ChannelSettings{MaxRetries: &zero})

	require.Equal(t, 0, selectedChannelMaxRetries(ginContext, &model.Channel{Id: 95}))
}
