package hailuo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

type hailuoRoundTripFunc func(*http.Request) (*http.Response, error)

func (f hailuoRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestBuildVideoURLInheritsPollingCancellation(t *testing.T) {
	var calls atomic.Int64
	var sawCanceledContext atomic.Bool
	adaptor := &TaskAdaptor{
		apiKey:  "test-key",
		baseURL: "https://upstream.example",
		httpClient: &http.Client{Transport: hailuoRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			sawCanceledContext.Store(request.Context().Err() == context.Canceled)
			return nil, request.Context().Err()
		})},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := adaptor.buildVideoURL(ctx, "task-id", "file-id")

	assert.Empty(t, result)
	assert.EqualValues(t, 1, calls.Load())
	assert.True(t, sawCanceledContext.Load(), "the follow-up request must inherit polling cancellation")
}

func TestBuildVideoURLRejectsOversizedProviderResponse(t *testing.T) {
	adaptor := &TaskAdaptor{
		apiKey:  "test-key",
		baseURL: "https://upstream.example",
		httpClient: &http.Client{Transport: hailuoRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", hailuoTaskResponseBodyLimit+1))),
				Request:    request,
			}, nil
		})},
	}

	result := adaptor.buildVideoURL(context.Background(), "task-id", "file-id")

	assert.Empty(t, result)
}
