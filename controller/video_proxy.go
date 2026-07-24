package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const (
	videoReadIdleTimeout     = 30 * time.Second
	defaultVideoProxyMaxSize = 64 << 20
)

var (
	errVideoReadIdleTimeout  = errors.New("video response read idle timeout")
	errVideoResponseTooLarge = errors.New("video response exceeds configured size limit")
	errVideoUpstreamStatus   = errors.New("video upstream returned an unsupported status")
	errVideoContentType      = errors.New("video upstream returned a non-video content type")
)

type videoReadResult struct {
	data []byte
	err  error
}

// videoProxyError returns a standardized OpenAI-style error response.
func videoProxyError(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
		},
	})
}

func VideoProxy(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Header("Allow", "GET, HEAD")
		videoProxyError(c, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}

	taskID := c.Param("task_id")
	if taskID == "" {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error", "task_id is required")
		return
	}

	userID := c.GetInt("id")
	task, exists, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to query task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Task not found")
		return
	}
	if task.Status != model.TaskStatusSuccess {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Task is not completed yet, current status: %s", task.Status))
		return
	}

	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to get channel for task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to retrieve channel information")
		return
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	videoURL := ""
	providerAPI := false
	requestHeaders := make(http.Header)
	switch channel.Type {
	case constant.ChannelTypeGemini:
		providerAPI = true
		apiKey := task.PrivateData.Key
		if apiKey == "" {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Missing stored API key for Gemini task %s", taskID))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "API key not stored for task")
			return
		}
		videoURL, err = getGeminiVideoURL(c.Request.Context(), channel, task, apiKey)
		requestHeaders.Set("x-goog-api-key", apiKey)
	case constant.ChannelTypeVertexAi:
		providerAPI = true
		videoURL, err = getVertexVideoURL(c.Request.Context(), channel, task)
	case constant.ChannelTypeOpenAI, constant.ChannelTypeSora:
		providerAPI = true
		videoURL = fmt.Sprintf("%s/v1/videos/%s/content", baseURL, task.GetUpstreamTaskID())
		requestHeaders.Set("Authorization", "Bearer "+channel.Key)
	default:
		// ResultURL is upstream-controlled content rather than a provider API
		// endpoint. It must always use the pinned-IP protected fetch client and
		// must never inherit the channel proxy.
		videoURL = task.GetResultURL()
	}
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve video URL for task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to resolve video URL")
		return
	}

	videoURL = strings.TrimSpace(videoURL)
	if videoURL == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video URL is empty for task %s", taskID))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}
	if len(videoURL) >= 5 && strings.EqualFold(videoURL[:5], "data:") {
		if err := writeVideoDataURL(c, videoURL); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to decode video data URL for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		}
		return
	}

	client := service.GetSSRFProtectedHTTPClient()
	if providerAPI {
		client, err = service.GetMediaHTTPClientWithProxy(channel.GetSetting().Proxy)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create media client for task %s: %s", taskID, err.Error()))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
			return
		}
	}
	if client == nil {
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Video client is not initialized")
		return
	}

	upstreamCtx, cancelUpstream := context.WithCancel(c.Request.Context())
	defer cancelUpstream()
	req, err := http.NewRequestWithContext(upstreamCtx, c.Request.Method, videoURL, nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create video request: %s", err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
		return
	}
	for key, values := range requestHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	for _, name := range []string{"Range", "If-Range"} {
		if value := c.GetHeader(name); value != "" {
			req.Header.Set(name, value)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to fetch video for task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}

	handled, prepareErr := prepareUpstreamVideoResponse(c, resp, maxVideoProxyBytes())
	if prepareErr != nil {
		_ = resp.Body.Close()
		logger.LogError(c.Request.Context(), fmt.Sprintf("Upstream returned status %d for task %s", resp.StatusCode, taskID))
		videoProxyError(c, http.StatusBadGateway, "server_error",
			prepareErr.Error())
		return
	}
	if handled {
		_ = resp.Body.Close()
		return
	}
	maxBytes := maxVideoProxyBytes()
	if c.Request.Method == http.MethodHead {
		_ = resp.Body.Close()
		return
	}
	if _, err := copyVideoBodyWithIdleTimeout(upstreamCtx, c.Writer, resp.Body, videoReadIdleTimeout, maxBytes); err != nil {
		cancelUpstream()
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to stream video content for task %s: %s", taskID, err.Error()))
	}
}

// prepareUpstreamVideoResponse validates the remote media envelope before any
// body bytes are exposed. handled is true for a forwarded 416 response.
func prepareUpstreamVideoResponse(c *gin.Context, resp *http.Response, maxBytes int64) (handled bool, err error) {
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		copyVideoResponseHeaders(c.Writer.Header(), resp.Header)
		c.Writer.Header().Del("Content-Type")
		c.Writer.Header().Set("Content-Length", "0")
		writeVideoSecurityHeaders(c.Writer.Header())
		c.Writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		c.Writer.WriteHeaderNow()
		return true, nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return false, fmt.Errorf("%w: %d", errVideoUpstreamStatus, resp.StatusCode)
	}
	if _, ok := allowedVideoMediaType(resp.Header.Get("Content-Type")); !ok {
		return false, errVideoContentType
	}
	if resp.ContentLength > maxBytes {
		return false, errVideoResponseTooLarge
	}
	copyVideoResponseHeaders(c.Writer.Header(), resp.Header)
	writeVideoSecurityHeaders(c.Writer.Header())
	c.Writer.WriteHeader(resp.StatusCode)
	c.Writer.WriteHeaderNow()
	return false, nil
}

func allowedVideoMediaType(raw string) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	mediaType = strings.ToLower(mediaType)
	if !strings.HasPrefix(mediaType, "video/") || strings.Contains(mediaType, "html") || strings.Contains(mediaType, "xml") || strings.Contains(mediaType, "svg") {
		return "", false
	}
	return mediaType, true
}

func maxVideoProxyBytes() int64 {
	if constant.MaxFileDownloadMB <= 0 {
		return defaultVideoProxyMaxSize
	}
	const bytesPerMiB = int64(1 << 20)
	maxInt64 := int64(^uint64(0) >> 1)
	megabytes := int64(constant.MaxFileDownloadMB)
	if megabytes > maxInt64/bytesPerMiB {
		return maxInt64
	}
	return megabytes * bytesPerMiB
}

func writeVideoSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "private, no-store")
	header.Set("X-Content-Type-Options", "nosniff")
}

func copyVideoResponseHeaders(dst, src http.Header) {
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		dst.Del(name)
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}

func copyVideoBodyWithIdleTimeout(ctx context.Context, dst io.Writer, body io.ReadCloser, idleTimeout time.Duration, maxBytes int64) (int64, error) {
	if idleTimeout <= 0 {
		idleTimeout = videoReadIdleTimeout
	}
	readCtx, cancel := context.WithCancel(ctx)
	results := make(chan videoReadResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(results)
		buffer := make([]byte, 32<<10)
		for {
			n, err := body.Read(buffer)
			result := videoReadResult{err: err}
			if n > 0 {
				result.data = append([]byte(nil), buffer[:n]...)
			}
			select {
			case results <- result:
			case <-readCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		_ = body.Close()
		<-done
	}()

	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	var written int64
	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		case <-timer.C:
			return written, errVideoReadIdleTimeout
		case result, ok := <-results:
			if !ok {
				return written, nil
			}
			if len(result.data) > 0 {
				if int64(len(result.data)) > maxBytes-written {
					return written, errVideoResponseTooLarge
				}
				n, writeErr := dst.Write(result.data)
				written += int64(n)
				if writeErr != nil {
					return written, writeErr
				}
				if n != len(result.data) {
					return written, io.ErrShortWrite
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idleTimeout)
			}
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					return written, nil
				}
				return written, result.err
			}
		}
	}
}

func writeVideoDataURL(c *gin.Context, dataURL string) error {
	comma := strings.IndexByte(dataURL, ',')
	if comma < 0 || len(dataURL) < 5 || !strings.EqualFold(dataURL[:5], "data:") {
		return fmt.Errorf("invalid data url")
	}
	header := dataURL[5:comma]
	if len(header) < len(";base64") || !strings.EqualFold(header[len(header)-len(";base64"):], ";base64") {
		return fmt.Errorf("unsupported data url encoding")
	}
	contentType := header[:len(header)-len(";base64")]
	if _, ok := allowedVideoMediaType(contentType); !ok {
		return fmt.Errorf("unsupported video content type")
	}

	payload := dataURL[comma+1:]
	maxBytes := maxVideoProxyBytes()
	maxInt64 := int64(^uint64(0) >> 1)
	maxEncoded := maxInt64
	if maxBytes <= (maxInt64-8)/4*3 {
		maxEncoded = ((maxBytes + 2) / 3 * 4) + 4
	}
	if int64(len(payload)) > maxEncoded {
		return errVideoResponseTooLarge
	}
	videoBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		videoBytes, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return err
		}
	}
	if int64(len(videoBytes)) > maxBytes {
		return errVideoResponseTooLarge
	}

	sum := sha256.Sum256(videoBytes)
	etag := fmt.Sprintf("\"%x\"", sum)
	responseHeader := c.Writer.Header()
	responseHeader.Set("Content-Type", contentType)
	responseHeader.Set("Accept-Ranges", "bytes")
	responseHeader.Set("ETag", etag)
	writeVideoSecurityHeaders(responseHeader)

	start, end, partial, rangeErr := parseVideoByteRange(c.GetHeader("Range"), c.GetHeader("If-Range"), etag, int64(len(videoBytes)))
	if rangeErr != nil {
		responseHeader.Set("Content-Range", fmt.Sprintf("bytes */%d", len(videoBytes)))
		responseHeader.Set("Content-Length", "0")
		c.Writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		c.Writer.WriteHeaderNow()
		return nil
	}
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		responseHeader.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(videoBytes)))
	}
	contentLength := end - start + 1
	responseHeader.Set("Content-Length", strconv.FormatInt(contentLength, 10))
	c.Writer.WriteHeader(status)
	c.Writer.WriteHeaderNow()
	if c.Request.Method == http.MethodHead || contentLength == 0 {
		return nil
	}
	_, err = c.Writer.Write(videoBytes[start : end+1])
	return err
}

func parseVideoByteRange(rangeHeader, ifRange, etag string, size int64) (start, end int64, partial bool, err error) {
	if rangeHeader == "" || (ifRange != "" && ifRange != etag) {
		if size == 0 {
			return 0, -1, false, nil
		}
		return 0, size - 1, false, nil
	}
	if size <= 0 || !strings.HasPrefix(rangeHeader, "bytes=") || strings.Contains(rangeHeader, ",") {
		return 0, 0, false, fmt.Errorf("invalid byte range")
	}
	value := strings.TrimSpace(strings.TrimPrefix(rangeHeader, "bytes="))
	parts := strings.SplitN(value, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, fmt.Errorf("invalid byte range")
	}
	if parts[0] == "" {
		suffix, parseErr := strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil || suffix <= 0 {
			return 0, 0, false, fmt.Errorf("invalid byte range")
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true, nil
	}
	start, parseErr := strconv.ParseInt(parts[0], 10, 64)
	if parseErr != nil || start < 0 || start >= size {
		return 0, 0, false, fmt.Errorf("invalid byte range")
	}
	end = size - 1
	if parts[1] != "" {
		end, parseErr = strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil || end < start {
			return 0, 0, false, fmt.Errorf("invalid byte range")
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true, nil
}
