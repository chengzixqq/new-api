package service

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixedModelHealthExecutor(client *http.Client, now time.Time) *modelHealthProbeExecutor {
	return &modelHealthProbeExecutor{
		localClient:    client,
		upstreamClient: client,
		now:            func() time.Time { return now },
		challenge: func() (modelHealthChallenge, error) {
			return modelHealthChallenge{prompt: "Return 42", expected: "42"}, nil
		},
		validateURL: func(string) error { return nil },
	}
}

func TestModelHealthProbeProtocolFixtures(t *testing.T) {
	originalSecret := common.CryptoSecret
	common.CryptoSecret = "probe-fixture-secret"
	t.Cleanup(func() { common.CryptoSecret = originalSecret })
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name         string
		protocol     string
		expectedPath string
		response     string
		bodyField    string
	}{
		{
			name: "OpenAI chat", protocol: model.ModelHealthProtocolOpenAIChat,
			expectedPath: "/v1/chat/completions", response: `{"choices":[{"message":{"content":"42"}}]}`,
			bodyField: "max_tokens",
		},
		{
			name: "OpenAI responses", protocol: model.ModelHealthProtocolOpenAIResponses,
			expectedPath: "/v1/responses", response: `{"output":[{"content":[{"type":"output_text","text":"42"}]}]}`,
			bodyField: "max_output_tokens",
		},
		{
			name: "Anthropic messages", protocol: model.ModelHealthProtocolAnthropicMessages,
			expectedPath: "/v1/messages", response: `{"content":[{"type":"text","text":"42"}]}`,
			bodyField: "max_tokens",
		},
		{
			name: "Gemini generateContent", protocol: model.ModelHealthProtocolGeminiContent,
			expectedPath: "/v1beta/models/gemini-test:generateContent", response: `{"candidates":[{"content":{"parts":[{"text":"42"}]}}]}`,
			bodyField: "generationConfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				assert.Equal(t, tt.expectedPath, request.URL.Path)
				assert.Equal(t, "Bearer sk-local-token", request.Header.Get("Authorization"))
				healthHeader := request.Header.Get(common.ModelHealthProbeHeader)
				assert.True(t, common.VerifyModelHealthProbeHeader(healthHeader, request.Method, request.URL.EscapedPath(), now))
				var body map[string]any
				require.NoError(t, common.DecodeJson(request.Body, &body))
				if tt.protocol == model.ModelHealthProtocolGeminiContent {
					assert.Nil(t, body["model"])
				} else {
					assert.Equal(t, "gemini-test", body["model"])
				}
				assert.Contains(t, body, tt.bodyField)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()
			executor := fixedModelHealthExecutor(server.Client(), now)

			result, err := executor.Probe(context.Background(), ModelHealthProbeConfig{
				Protocol: tt.protocol,
				BaseURL:  server.URL,
				APIKey:   "local-token",
				Model:    "gemini-test",
				Timeout:  time.Second,
				Local:    true,
			})

			require.NoError(t, err)
			assert.Equal(t, model.ModelHealthProbeSuccess, result.Status)
			assert.Empty(t, result.ErrorClass)
			assert.Equal(t, http.StatusOK, result.HTTPStatus)
		})
	}
}

func TestModelHealthProbeUpstreamAuthenticationHeaders(t *testing.T) {
	tests := []struct {
		protocol string
		header   string
		value    string
	}{
		{model.ModelHealthProtocolOpenAIChat, "Authorization", "Bearer upstream-key"},
		{model.ModelHealthProtocolOpenAIResponses, "Authorization", "Bearer upstream-key"},
		{model.ModelHealthProtocolAnthropicMessages, "x-api-key", "upstream-key"},
		{model.ModelHealthProtocolGeminiContent, "x-goog-api-key", "upstream-key"},
	}
	for _, tt := range tests {
		t.Run(tt.protocol, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				assert.Equal(t, tt.value, request.Header.Get(tt.header))
				assert.Empty(t, request.Header.Get(common.ModelHealthProbeHeader))
				switch tt.protocol {
				case model.ModelHealthProtocolOpenAIChat:
					_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"42"}}]}`))
				case model.ModelHealthProtocolOpenAIResponses:
					_, _ = w.Write([]byte(`{"output_text":"42"}`))
				case model.ModelHealthProtocolAnthropicMessages:
					_, _ = w.Write([]byte(`{"content":[{"text":"42"}]}`))
				case model.ModelHealthProtocolGeminiContent:
					_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"42"}]}}]}`))
				}
			}))
			defer server.Close()
			executor := fixedModelHealthExecutor(server.Client(), time.Unix(1_700_000_000, 0))
			result, err := executor.Probe(context.Background(), ModelHealthProbeConfig{
				Protocol: tt.protocol, BaseURL: server.URL, APIKey: "upstream-key",
				Model: "probe-model", Timeout: time.Second,
			})
			require.NoError(t, err)
			assert.Equal(t, model.ModelHealthProbeSuccess, result.Status)
		})
	}
}

func TestModelHealthProbeClassifiesInvalidAndHTTPResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		status     string
		errorClass string
	}{
		{"invalid 2xx", 200, `{"choices":[{"message":{"content":"41"}}]}`, model.ModelHealthProbeInvalidResponse, ModelHealthErrorInvalidResponse},
		{"authentication", 401, `{"error":"secret upstream message"}`, model.ModelHealthProbeAuthFailure, ModelHealthErrorAuthentication},
		{"rate limit", 429, ``, model.ModelHealthProbeFailure, ModelHealthErrorRateLimited},
		{"server error", 503, ``, model.ModelHealthProbeFailure, ModelHealthErrorServer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			executor := fixedModelHealthExecutor(server.Client(), time.Unix(1_700_000_000, 0))
			result, err := executor.Probe(context.Background(), ModelHealthProbeConfig{
				Protocol: model.ModelHealthProtocolOpenAIChat, BaseURL: server.URL,
				APIKey: "local", Model: "model", Timeout: time.Second, Local: true,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.status, result.Status)
			assert.Equal(t, tt.errorClass, result.ErrorClass)
			assert.NotContains(t, result.ErrorClass, "secret upstream message")
		})
	}
}

func TestModelHealthProbeTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"42"}}]}`))
	}))
	defer server.Close()
	executor := fixedModelHealthExecutor(server.Client(), time.Now())

	result, err := executor.Probe(context.Background(), ModelHealthProbeConfig{
		Protocol: model.ModelHealthProtocolOpenAIChat, BaseURL: server.URL,
		APIKey: "local", Model: "model", Timeout: 10 * time.Millisecond, Local: true,
	})

	require.NoError(t, err)
	assert.Equal(t, model.ModelHealthProbeTimeout, result.Status)
	assert.Equal(t, ModelHealthErrorTimeout, result.ErrorClass)
}

func TestValidateModelHealthUpstreamURLRequiresPublicHTTPSShape(t *testing.T) {
	assert.NoError(t, ValidateModelHealthUpstreamURL("https://api.example.com/v1"))
	for _, candidate := range []string{
		"http://api.example.com/v1",
		"https://user:password@api.example.com/v1",
		"https://api.example.com/v1?key=secret",
		"https://127.0.0.1/v1",
		"https://[::1]/v1",
		"not a URL",
	} {
		assert.Error(t, ValidateModelHealthUpstreamURL(candidate), candidate)
	}
}

func TestModelHealthUpstreamClientRejectsRedirectsAndPrivateTargets(t *testing.T) {
	executor := newModelHealthProbeExecutor()
	transport, ok := executor.upstreamClient.Transport.(*ssrfProtectedRoundTripper)
	require.True(t, ok)
	assert.Equal(t, 5*time.Minute, transport.responseHeaderTimeout)
	require.NotNil(t, transport.tlsConfig)
	assert.False(t, transport.tlsConfig.InsecureSkipVerify)
	assert.Equal(t, uint16(tls.VersionTLS12), transport.tlsConfig.MinVersion)
	request, err := http.NewRequest(http.MethodPost, "https://api.example.com/v1/chat/completions", nil)
	require.NoError(t, err)
	require.Error(t, executor.upstreamClient.CheckRedirect(request, nil))

	protection, enabled, err := strictModelHealthProtection()
	require.NoError(t, err)
	require.True(t, enabled)
	assert.Error(t, protection.ValidateNetworkTarget("127.0.0.1", 443))
	assert.Error(t, protection.ValidateNetworkTarget("169.254.169.254", 443))
	assert.NoError(t, protection.ValidateNetworkTarget("8.8.8.8", 443))
}

func TestModelHealthProbeEndpointAvoidsVersionDuplication(t *testing.T) {
	endpoint, err := modelHealthProbeEndpoint("https://api.example.com/v1", model.ModelHealthProtocolOpenAIChat, "model")
	require.NoError(t, err)
	assert.Equal(t, "https://api.example.com/v1/chat/completions", endpoint)

	endpoint, err = modelHealthProbeEndpoint("https://api.example.com/prefix/v1/responses", model.ModelHealthProtocolOpenAIResponses, "model")
	require.NoError(t, err)
	assert.Equal(t, "https://api.example.com/prefix/v1/responses", endpoint)

	endpoint, err = modelHealthProbeEndpoint("https://api.example.com", model.ModelHealthProtocolGeminiContent, "publishers/google/gemini-test")
	require.NoError(t, err)
	assert.True(t, strings.Contains(endpoint, "/v1beta/models/publishers%2Fgoogle%2Fgemini-test:generateContent"), endpoint)
	assert.NotContains(t, endpoint, "%252F")
}
