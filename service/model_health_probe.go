package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const modelHealthMaxResponseBytes = 64 * 1024

const (
	ModelHealthErrorNone            = ""
	ModelHealthErrorNetwork         = "network_error"
	ModelHealthErrorTimeout         = "timeout"
	ModelHealthErrorAuthentication  = "authentication"
	ModelHealthErrorRateLimited     = "rate_limited"
	ModelHealthErrorClient          = "client_error"
	ModelHealthErrorServer          = "server_error"
	ModelHealthErrorRedirect        = "redirect"
	ModelHealthErrorInvalidResponse = "invalid_response"
	ModelHealthErrorCredential      = "credential_unavailable"
)

type ModelHealthProbeConfig struct {
	Protocol string
	BaseURL  string
	APIKey   string
	Model    string
	Timeout  time.Duration
	Local    bool
}

type ModelHealthProbeResult struct {
	Model      string `json:"model"`
	Status     string `json:"status"`
	LatencyMs  int64  `json:"latency_ms"`
	HTTPStatus int    `json:"http_status"`
	ErrorClass string `json:"error_class,omitempty"`
	CheckedAt  int64  `json:"checked_at"`
}

type modelHealthChallenge struct {
	prompt   string
	expected string
}

type modelHealthProbeExecutor struct {
	localClient    *http.Client
	upstreamClient *http.Client
	now            func() time.Time
	challenge      func() (modelHealthChallenge, error)
	validateURL    func(string) error
}

func newModelHealthProbeExecutor() *modelHealthProbeExecutor {
	upstreamClient := newProtectedFetchHTTPClientWithDialer(nil, nil, strictModelHealthProtection)
	if transport, ok := upstreamClient.Transport.(*ssrfProtectedRoundTripper); ok {
		// The per-request context owns the configured 1-300 second probe
		// timeout. Avoid inheriting the media fetcher's shorter 30 second
		// response-header cap when the model-health default is 45 seconds.
		transport.responseHeaderTimeout = 5 * time.Minute
		// Upstream health credentials are only sent over certificate-verified
		// TLS, independent of the relay-wide insecure compatibility switch.
		transport.tlsConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ClientSessionCache: tls.NewLRUClientSessionCache(0),
		}
	}
	upstreamClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errors.New("model health probe redirects are not allowed")
	}
	return &modelHealthProbeExecutor{
		localClient:    GetHttpClient(),
		upstreamClient: upstreamClient,
		now:            time.Now,
		challenge:      newModelHealthChallenge,
		validateURL:    ValidateModelHealthUpstreamURL,
	}
}

func strictModelHealthProtection() (*common.SSRFProtection, bool, error) {
	return &common.SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		ApplyIPFilterForDomain: true,
	}, true, nil
}

func ValidateModelHealthUpstreamURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("invalid upstream URL")
	}
	if parsed.Scheme != "https" {
		return errors.New("upstream URL must use HTTPS")
	}
	if parsed.User != nil {
		return errors.New("upstream URL must not contain user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("upstream URL must not contain a query or fragment")
	}
	port := 443
	if parsed.Port() != "" {
		parsedPort, parseErr := strconv.Atoi(parsed.Port())
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return errors.New("invalid upstream URL port")
		}
		port = parsedPort
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil {
		protection, _, _ := strictModelHealthProtection()
		if protection == nil || protection.ValidateNetworkTarget(ip.String(), port) != nil {
			return errors.New("upstream URL target is not public")
		}
	}
	return nil
}

func ExecuteModelHealthProbe(ctx context.Context, config ModelHealthProbeConfig) (ModelHealthProbeResult, error) {
	return newModelHealthProbeExecutor().Probe(ctx, config)
}

func (executor *modelHealthProbeExecutor) Probe(ctx context.Context, config ModelHealthProbeConfig) (ModelHealthProbeResult, error) {
	result := ModelHealthProbeResult{Model: config.Model, Status: model.ModelHealthProbeFailure}
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.Model = strings.TrimSpace(config.Model)
	if !model.ValidModelHealthProtocol(config.Protocol) {
		return result, errors.New("unsupported model health protocol")
	}
	if config.BaseURL == "" || config.APIKey == "" || config.Model == "" {
		return result, errors.New("incomplete model health probe configuration")
	}
	if !config.Local {
		if executor.validateURL == nil {
			return result, errors.New("model health upstream URL validator is unavailable")
		}
		if err := executor.validateURL(config.BaseURL); err != nil {
			return result, err
		}
	}
	if config.Timeout <= 0 || config.Timeout > 5*time.Minute {
		return result, errors.New("model health probe timeout must be between 1 second and 5 minutes")
	}

	challenge, err := executor.challenge()
	if err != nil {
		return result, errors.New("failed to create model health challenge")
	}
	endpoint, err := modelHealthProbeEndpoint(config.BaseURL, config.Protocol, config.Model)
	if err != nil {
		return result, err
	}
	body, err := modelHealthProbeBody(config.Protocol, config.Model, challenge.prompt)
	if err != nil {
		return result, err
	}

	requestContext, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, errors.New("failed to create model health probe request")
	}
	request.Header.Set("Content-Type", "application/json")
	if config.Local {
		request.Header.Set("Authorization", "Bearer sk-"+strings.TrimPrefix(config.APIKey, "sk-"))
		signature, err := common.NewModelHealthProbeHeader(request.Method, request.URL.EscapedPath(), executor.now())
		if err != nil {
			return result, errors.New("failed to sign model health probe request")
		}
		request.Header.Set(common.ModelHealthProbeHeader, signature)
	} else {
		switch config.Protocol {
		case model.ModelHealthProtocolAnthropicMessages:
			request.Header.Set("x-api-key", config.APIKey)
			request.Header.Set("anthropic-version", "2023-06-01")
		case model.ModelHealthProtocolGeminiContent:
			request.Header.Set("x-goog-api-key", config.APIKey)
		default:
			request.Header.Set("Authorization", "Bearer "+config.APIKey)
		}
	}

	client := executor.upstreamClient
	if config.Local {
		client = executor.localClient
	}
	if client == nil {
		return result, errors.New("model health HTTP client is not initialized")
	}
	startedAt := executor.now()
	response, requestErr := client.Do(request)
	finishedAt := executor.now()
	result.CheckedAt = finishedAt.Unix()
	result.LatencyMs = finishedAt.Sub(startedAt).Milliseconds()
	if result.LatencyMs < 0 {
		result.LatencyMs = 0
	}
	if requestErr != nil {
		var networkError net.Error
		if errors.Is(requestErr, context.DeadlineExceeded) ||
			errors.Is(requestContext.Err(), context.DeadlineExceeded) ||
			(errors.As(requestErr, &networkError) && networkError.Timeout()) {
			result.Status = model.ModelHealthProbeTimeout
			result.ErrorClass = ModelHealthErrorTimeout
		} else {
			result.Status = model.ModelHealthProbeFailure
			result.ErrorClass = ModelHealthErrorNetwork
		}
		return result, nil
	}
	defer response.Body.Close()
	result.HTTPStatus = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Status, result.ErrorClass = classifyModelHealthHTTPStatus(response.StatusCode)
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, modelHealthMaxResponseBytes))
		return result, nil
	}

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, modelHealthMaxResponseBytes+1))
	if err != nil || len(responseBody) > modelHealthMaxResponseBytes {
		result.Status = model.ModelHealthProbeInvalidResponse
		result.ErrorClass = ModelHealthErrorInvalidResponse
		return result, nil
	}
	answer, ok := extractModelHealthAnswer(config.Protocol, responseBody)
	if !ok || !ValidateTextProbeAnswer(answer, challenge.expected) {
		result.Status = model.ModelHealthProbeInvalidResponse
		result.ErrorClass = ModelHealthErrorInvalidResponse
		return result, nil
	}
	result.Status = model.ModelHealthProbeSuccess
	result.ErrorClass = ModelHealthErrorNone
	return result, nil
}

func newModelHealthChallenge() (modelHealthChallenge, error) {
	left, err := rand.Int(rand.Reader, big.NewInt(90))
	if err != nil {
		return modelHealthChallenge{}, err
	}
	right, err := rand.Int(rand.Reader, big.NewInt(90))
	if err != nil {
		return modelHealthChallenge{}, err
	}
	a := left.Int64() + 10
	b := right.Int64() + 10
	return modelHealthChallenge{
		prompt:   fmt.Sprintf("Return only the integer result of %d + %d. Do not include any other text.", a, b),
		expected: strconv.FormatInt(a+b, 10),
	}, nil
}

func modelHealthProbeEndpoint(baseURL, protocol, modelName string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "", errors.New("invalid model health endpoint")
	}
	var suffix string
	rawSuffix := ""
	switch protocol {
	case model.ModelHealthProtocolOpenAIChat:
		suffix = "/v1/chat/completions"
	case model.ModelHealthProtocolOpenAIResponses:
		suffix = "/v1/responses"
	case model.ModelHealthProtocolAnthropicMessages:
		suffix = "/v1/messages"
	case model.ModelHealthProtocolGeminiContent:
		suffix = "/v1beta/models/" + modelName + ":generateContent"
		rawSuffix = "/v1beta/models/" + url.PathEscape(modelName) + ":generateContent"
	default:
		return "", errors.New("unsupported model health protocol")
	}

	path := strings.TrimRight(parsed.Path, "/")
	rawPath := strings.TrimRight(parsed.EscapedPath(), "/")
	if !modelHealthPathAlreadyComplete(path, protocol) {
		versionPrefix := "/" + strings.Split(strings.TrimPrefix(suffix, "/"), "/")[0]
		if strings.HasSuffix(path, versionPrefix) {
			suffix = strings.TrimPrefix(suffix, versionPrefix)
			if rawSuffix != "" {
				rawSuffix = strings.TrimPrefix(rawSuffix, versionPrefix)
			}
		}
		parsed.Path = path + suffix
		if rawSuffix != "" {
			parsed.RawPath = rawPath + rawSuffix
		} else {
			parsed.RawPath = ""
		}
	}
	return parsed.String(), nil
}

func modelHealthPathAlreadyComplete(path, protocol string) bool {
	switch protocol {
	case model.ModelHealthProtocolOpenAIChat:
		return strings.HasSuffix(path, "/chat/completions")
	case model.ModelHealthProtocolOpenAIResponses:
		return strings.HasSuffix(path, "/responses")
	case model.ModelHealthProtocolAnthropicMessages:
		return strings.HasSuffix(path, "/messages")
	case model.ModelHealthProtocolGeminiContent:
		return strings.HasSuffix(path, ":generateContent")
	default:
		return false
	}
}

func modelHealthProbeBody(protocol, modelName, prompt string) ([]byte, error) {
	request, err := NewTextProbeRequest(modelName, prompt, false, 16)
	if err != nil {
		return nil, err
	}
	payload, err := request.DirectPayload(protocol)
	if err != nil {
		return nil, err
	}
	return common.Marshal(payload)
}

func classifyModelHealthHTTPStatus(status int) (string, string) {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return model.ModelHealthProbeAuthFailure, ModelHealthErrorAuthentication
	case status == http.StatusTooManyRequests:
		return model.ModelHealthProbeFailure, ModelHealthErrorRateLimited
	case status >= 300 && status < 400:
		return model.ModelHealthProbeFailure, ModelHealthErrorRedirect
	case status >= 400 && status < 500:
		return model.ModelHealthProbeFailure, ModelHealthErrorClient
	default:
		return model.ModelHealthProbeFailure, ModelHealthErrorServer
	}
}

func extractModelHealthAnswer(protocol string, body []byte) (string, bool) {
	switch protocol {
	case model.ModelHealthProtocolOpenAIChat:
		var response struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if common.Unmarshal(body, &response) != nil || len(response.Choices) == 0 {
			return "", false
		}
		return response.Choices[0].Message.Content, true
	case model.ModelHealthProtocolOpenAIResponses:
		var response struct {
			OutputText string `json:"output_text"`
			Output     []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		}
		if common.Unmarshal(body, &response) != nil {
			return "", false
		}
		if response.OutputText != "" {
			return response.OutputText, true
		}
		for _, output := range response.Output {
			for _, content := range output.Content {
				if content.Text != "" {
					return content.Text, true
				}
			}
		}
		return "", false
	case model.ModelHealthProtocolAnthropicMessages:
		var response struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if common.Unmarshal(body, &response) != nil || len(response.Content) == 0 {
			return "", false
		}
		return response.Content[0].Text, true
	case model.ModelHealthProtocolGeminiContent:
		var response struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if common.Unmarshal(body, &response) != nil || len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
			return "", false
		}
		return response.Candidates[0].Content.Parts[0].Text, true
	default:
		return "", false
	}
}
