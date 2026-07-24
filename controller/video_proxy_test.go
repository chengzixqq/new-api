package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newVideoTestContext(method string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, "/v1/videos/task/content", nil)
	return ctx, recorder
}

func TestWriteVideoDataURLHTTPContract(t *testing.T) {
	payload := []byte("0123456789")
	dataURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(payload)

	t.Run("range", func(t *testing.T) {
		ctx, recorder := newVideoTestContext(http.MethodGet)
		ctx.Request.Header.Set("Range", "bytes=2-5")

		require.NoError(t, writeVideoDataURL(ctx, dataURL))

		assert.Equal(t, http.StatusPartialContent, recorder.Code)
		assert.Equal(t, "2345", recorder.Body.String())
		assert.Equal(t, "bytes 2-5/10", recorder.Header().Get("Content-Range"))
		assert.Equal(t, "4", recorder.Header().Get("Content-Length"))
		assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
		assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	})

	t.Run("head", func(t *testing.T) {
		ctx, recorder := newVideoTestContext(http.MethodHead)

		require.NoError(t, writeVideoDataURL(ctx, dataURL))

		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Empty(t, recorder.Body.String())
		assert.Equal(t, "10", recorder.Header().Get("Content-Length"))
	})

	t.Run("if-range mismatch falls back to full response", func(t *testing.T) {
		ctx, recorder := newVideoTestContext(http.MethodGet)
		ctx.Request.Header.Set("Range", "bytes=2-5")
		ctx.Request.Header.Set("If-Range", `"different"`)

		require.NoError(t, writeVideoDataURL(ctx, dataURL))

		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, string(payload), recorder.Body.String())
	})

	t.Run("unsatisfied range", func(t *testing.T) {
		ctx, recorder := newVideoTestContext(http.MethodGet)
		ctx.Request.Header.Set("Range", "bytes=99-100")

		require.NoError(t, writeVideoDataURL(ctx, dataURL))

		assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, recorder.Code)
		assert.Empty(t, recorder.Body.String())
		assert.Equal(t, "bytes */10", recorder.Header().Get("Content-Range"))
	})
}

func TestWriteVideoDataURLRejectsUnsafeTypeAndOversize(t *testing.T) {
	unsafePayload := base64.StdEncoding.EncodeToString([]byte("<html>bad</html>"))
	ctx, _ := newVideoTestContext(http.MethodGet)
	require.Error(t, writeVideoDataURL(ctx, "data:text/html;base64,"+unsafePayload))
	ctx, _ = newVideoTestContext(http.MethodGet)
	require.Error(t, writeVideoDataURL(ctx, "data:video/svg+xml;base64,"+unsafePayload))

	oldLimit := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() { constant.MaxFileDownloadMB = oldLimit })
	large := bytes.Repeat([]byte("x"), (1<<20)+1)
	ctx, _ = newVideoTestContext(http.MethodGet)
	err := writeVideoDataURL(ctx, "data:video/mp4;base64,"+base64.StdEncoding.EncodeToString(large))
	require.ErrorIs(t, err, errVideoResponseTooLarge)
}

func TestPrepareUpstreamVideoResponseStatusesAndHeaders(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		handled     bool
		wantErr     error
	}{
		{name: "ok", status: http.StatusOK, contentType: "video/mp4"},
		{name: "partial", status: http.StatusPartialContent, contentType: "video/webm"},
		{name: "range unsatisfied", status: http.StatusRequestedRangeNotSatisfiable, handled: true},
		{name: "reject html", status: http.StatusOK, contentType: "text/html", wantErr: errVideoContentType},
		{name: "reject status", status: http.StatusFound, contentType: "video/mp4", wantErr: errVideoUpstreamStatus},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newVideoTestContext(http.MethodGet)
			resp := &http.Response{
				StatusCode:    test.status,
				ContentLength: 5,
				Header: http.Header{
					"Content-Type":  []string{test.contentType},
					"Content-Range": []string{"bytes 0-4/10"},
					"Set-Cookie":    []string{"secret=1"},
					"X-Upstream":    []string{"private"},
				},
				Body: io.NopCloser(strings.NewReader("video")),
			}

			handled, err := prepareUpstreamVideoResponse(ctx, resp, 100)

			assert.Equal(t, test.handled, handled)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				assert.Empty(t, recorder.Header().Get("Set-Cookie"))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.status, recorder.Code)
			assert.Empty(t, recorder.Header().Get("Set-Cookie"))
			assert.Empty(t, recorder.Header().Get("X-Upstream"))
			if test.status == http.StatusRequestedRangeNotSatisfiable {
				assert.Empty(t, recorder.Header().Get("Content-Type"))
				assert.Equal(t, "0", recorder.Header().Get("Content-Length"))
			}
			assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
			assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
		})
	}
}

func TestPrepareUpstreamVideoResponseRejectsDeclaredOversize(t *testing.T) {
	ctx, _ := newVideoTestContext(http.MethodGet)
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: 101,
		Header:        http.Header{"Content-Type": []string{"video/mp4"}},
		Body:          io.NopCloser(strings.NewReader("video")),
	}

	_, err := prepareUpstreamVideoResponse(ctx, resp, 100)

	require.ErrorIs(t, err, errVideoResponseTooLarge)
}

func TestCopyVideoBodyWithIdleTimeout(t *testing.T) {
	t.Run("continuous data is not cut off", func(t *testing.T) {
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			for _, chunk := range []string{"one", "two", "three"} {
				_, _ = writer.Write([]byte(chunk))
				time.Sleep(10 * time.Millisecond)
			}
		}()
		var output bytes.Buffer

		written, err := copyVideoBodyWithIdleTimeout(context.Background(), &output, reader, 40*time.Millisecond, 100)

		require.NoError(t, err)
		assert.Equal(t, int64(11), written)
		assert.Equal(t, "onetwothree", output.String())
	})

	t.Run("idle read closes the source", func(t *testing.T) {
		reader, writer := io.Pipe()
		defer writer.Close()
		started := time.Now()

		_, err := copyVideoBodyWithIdleTimeout(context.Background(), io.Discard, reader, 20*time.Millisecond, 100)

		require.ErrorIs(t, err, errVideoReadIdleTimeout)
		assert.Less(t, time.Since(started), time.Second)
	})

	t.Run("streamed body is bounded", func(t *testing.T) {
		body := io.NopCloser(strings.NewReader("1234"))

		written, err := copyVideoBodyWithIdleTimeout(context.Background(), io.Discard, body, time.Second, 3)

		require.ErrorIs(t, err, errVideoResponseTooLarge)
		assert.Zero(t, written)
	})

	t.Run("caller cancellation propagates", func(t *testing.T) {
		reader, writer := io.Pipe()
		defer writer.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := copyVideoBodyWithIdleTimeout(ctx, io.Discard, reader, time.Second, 100)

		require.True(t, errors.Is(err, context.Canceled))
	})
}

func TestAllowedVideoMediaType(t *testing.T) {
	tests := []struct {
		value string
		ok    bool
	}{
		{value: "video/mp4", ok: true},
		{value: "video/webm; codecs=vp9", ok: true},
		{value: "text/html"},
		{value: "application/xml"},
		{value: "image/svg+xml"},
		{value: "video/svg+xml"},
		{value: ""},
	}
	for _, test := range tests {
		_, ok := allowedVideoMediaType(test.value)
		assert.Equal(t, test.ok, ok, test.value)
	}
}
