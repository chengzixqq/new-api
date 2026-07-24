package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTextProbeRequestSharesRelayAndDirectSemantics(t *testing.T) {
	request, err := NewTextProbeRequest("probe-model", "Return 42", true, 16)
	require.NoError(t, err)

	relayRequest, err := request.RelayRequest(TextProbeProtocolOpenAIResponses)
	require.NoError(t, err)
	responses, ok := relayRequest.(*dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Equal(t, "probe-model", responses.Model)
	assert.Equal(t, uint(16), *responses.MaxOutputTokens)
	assert.True(t, *responses.Stream)
	assert.True(t, responses.StreamOptions.IncludeUsage)
	assert.Contains(t, string(responses.Input), "Return 42")

	payload, err := request.DirectPayload(TextProbeProtocolOpenAIResponses)
	require.NoError(t, err)
	encoded, err := common.Marshal(payload)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"model":"probe-model"`)
	assert.Contains(t, string(encoded), `"max_output_tokens":16`)
	assert.Contains(t, string(encoded), `"input":"Return 42"`)
}

func TestValidateTextProbeAnswerRequiresExactNonEmptyResult(t *testing.T) {
	assert.True(t, ValidateTextProbeAnswer(" 42\n", "42"))
	assert.False(t, ValidateTextProbeAnswer("the result is 42", "42"))
	assert.False(t, ValidateTextProbeAnswer("", ""))
}
