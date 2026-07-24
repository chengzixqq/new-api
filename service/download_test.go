package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDoDownloadRequestWithContextSetsDeadlineAndCancelsOnClose(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	originalProtectedClient := ssrfProtectedHTTPClient
	originalWorkerURL := system_setting.WorkerUrl
	originalWorkerKey := system_setting.WorkerValidKey
	t.Cleanup(func() {
		*fetchSetting = originalFetchSetting
		ssrfProtectedHTTPClient = originalProtectedClient
		system_setting.WorkerUrl = originalWorkerURL
		system_setting.WorkerValidKey = originalWorkerKey
	})

	fetchSetting.EnableSSRFProtection = false
	system_setting.WorkerUrl = ""
	system_setting.WorkerValidKey = ""

	var requestContext context.Context
	ssrfProtectedHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestContext = req.Context()
		deadline, ok := requestContext.Deadline()
		require.True(t, ok)
		remaining := time.Until(deadline)
		require.Greater(t, remaining, mediaDownloadTimeout-time.Second)
		require.LessOrEqual(t, remaining, mediaDownloadTimeout)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("image")),
			Request:    req,
		}, nil
	})}

	resp, err := DoDownloadRequestWithContext(context.Background(), "https://example.com/image.png")
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NoError(t, resp.Body.Close())

	select {
	case <-requestContext.Done():
		require.ErrorIs(t, requestContext.Err(), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("download context was not canceled when the response body closed")
	}
}

func TestDoDownloadRequestWithContextPropagatesCallerCancellation(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	originalProtectedClient := ssrfProtectedHTTPClient
	originalWorkerURL := system_setting.WorkerUrl
	originalWorkerKey := system_setting.WorkerValidKey
	t.Cleanup(func() {
		*fetchSetting = originalFetchSetting
		ssrfProtectedHTTPClient = originalProtectedClient
		system_setting.WorkerUrl = originalWorkerURL
		system_setting.WorkerValidKey = originalWorkerKey
	})

	fetchSetting.EnableSSRFProtection = false
	system_setting.WorkerUrl = ""
	system_setting.WorkerValidKey = ""
	ssrfProtectedHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err := DoDownloadRequestWithContext(ctx, "https://example.com/image.png")

	require.Nil(t, resp)
	require.ErrorIs(t, err, context.Canceled)
}
