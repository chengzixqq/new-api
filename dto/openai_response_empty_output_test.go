package dto

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesResponseUsableOutput(t *testing.T) {
	tests := []struct {
		name   string
		output []ResponsesOutput
		usable bool
	}{
		{name: "empty", usable: false},
		{name: "reasoning only", output: []ResponsesOutput{{Type: ResponsesOutputTypeReasoning}}, usable: false},
		{name: "empty message", output: []ResponsesOutput{{Type: ResponsesOutputTypeMessage}}, usable: false},
		{name: "whitespace text", output: []ResponsesOutput{{Type: ResponsesOutputTypeMessage, Content: []ResponsesOutputContent{{Type: "output_text", Text: "  "}}}}, usable: false},
		{name: "text", output: []ResponsesOutput{{Type: ResponsesOutputTypeMessage, Content: []ResponsesOutputContent{{Type: "output_text", Text: "done"}}}}, usable: true},
		{name: "refusal", output: []ResponsesOutput{{Type: ResponsesOutputTypeMessage, Content: []ResponsesOutputContent{{Type: "refusal", Refusal: "declined"}}}}, usable: true},
		{name: "function call", output: []ResponsesOutput{{Type: "function_call", Name: "lookup"}}, usable: true},
		{name: "image call", output: []ResponsesOutput{{Type: ResponsesOutputTypeImageGenerationCall}}, usable: true},
		{name: "future output", output: []ResponsesOutput{{Type: "future_output_type"}}, usable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := OpenAIResponsesResponse{Status: json.RawMessage(`"completed"`), Output: tt.output}
			assert.Equal(t, tt.usable, response.HasUsableOutput())
			assert.Equal(t, !tt.usable, response.IsCompletedWithoutUsableOutput())
		})
	}
}

func TestOpenAIResponsesResponseReasoningEncryptedContentIsNotUsable(t *testing.T) {
	payload := []byte(`{"id":"resp_test","status":"completed","output":[{"type":"reasoning","content":[],"encrypted_content":"secret","summary":[]}],"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`)
	var response OpenAIResponsesResponse
	require.NoError(t, common.Unmarshal(payload, &response))
	require.True(t, response.IsCompletedWithoutUsableOutput())
}

func TestOpenAIResponsesResponseOnlyCompletedCanBeRejected(t *testing.T) {
	response := OpenAIResponsesResponse{Status: json.RawMessage(`"incomplete"`)}
	require.False(t, response.IsCompletedWithoutUsableOutput())
}
