package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConservativeInterruptedStreamUsage(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{ReceivedResponseCount: 3}
	info.SetEstimatePromptTokens(11)
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.MarkSSELimitExceeded(assert.AnError)
	info.MarkStreamOutputStarted()

	usage := conservativeInterruptedStreamUsage(ctx, info, &dto.Usage{})

	require.NotNil(t, usage)
	assert.Equal(t, 11, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, 14, usage.TotalTokens)
	assert.Equal(t, 11, usage.PromptTokensDetails.TextTokens)
	assert.Equal(t, 3, usage.CompletionTokenDetails.TextTokens)
}

func TestConservativeInterruptedStreamUsagePreservesReportedUsage(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{}
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.MarkSSELimitExceeded(assert.AnError)
	info.MarkStreamOutputStarted()
	original := &dto.Usage{PromptTokens: 5, CompletionTokens: 7, TotalTokens: 12}

	assert.Same(t, original, conservativeInterruptedStreamUsage(ctx, info, original))
}
