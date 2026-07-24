package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
