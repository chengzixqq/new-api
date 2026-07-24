package relay

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMidjourneyImageSignedURLBindsUserTaskAndExpiry(t *testing.T) {
	oldSecret := common.CryptoSecret
	oldServerAddress := system_setting.ServerAddress
	common.CryptoSecret = "midjourney-image-test-secret"
	system_setting.ServerAddress = "https://newapi.example/"
	t.Cleanup(func() {
		common.CryptoSecret = oldSecret
		system_setting.ServerAddress = oldServerAddress
	})

	now := time.Unix(1_800_000_000, 0)
	task := &model.Midjourney{UserId: 42, MjId: "mj-task-1"}
	signedURL := buildMidjourneyImageURL(task, now)
	parsed, err := url.Parse(signedURL)
	require.NoError(t, err)
	assert.Equal(t, "/mj/image/mj-task-1", parsed.Path)
	assert.Equal(t, "42", parsed.Query().Get("user_id"))

	expires, err := strconv.ParseInt(parsed.Query().Get("expires"), 10, 64)
	require.NoError(t, err)
	assert.Equal(t, now.Add(midjourneyImageURLTTL).Unix(), expires)
	signature := parsed.Query().Get("signature")
	assert.True(t, validMidjourneyImageSignature(task.UserId, task.MjId, expires, signature))
	assert.False(t, validMidjourneyImageSignature(task.UserId+1, task.MjId, expires, signature))
	assert.False(t, validMidjourneyImageSignature(task.UserId, task.MjId+"-tampered", expires, signature))
	assert.False(t, validMidjourneyImageSignature(task.UserId, task.MjId, expires+1, signature))
	assert.False(t, validMidjourneyImageSignature(task.UserId, task.MjId, expires, signature+"00"))
}

func TestRelayMidjourneyImageRequiresValidUserBoundSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	oldSecret := common.CryptoSecret
	oldLimit := constant.MaxFileDownloadMB
	fetchSetting := system_setting.GetFetchSetting()
	oldFetchSetting := *fetchSetting
	oldFetchSetting.DomainList = append([]string(nil), fetchSetting.DomainList...)
	oldFetchSetting.IpList = append([]string(nil), fetchSetting.IpList...)
	oldFetchSetting.AllowedPorts = append([]string(nil), fetchSetting.AllowedPorts...)
	t.Cleanup(func() {
		model.DB = oldDB
		common.CryptoSecret = oldSecret
		constant.MaxFileDownloadMB = oldLimit
		*fetchSetting = oldFetchSetting
	})

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:mj-image-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Midjourney{}))
	model.DB = db
	common.CryptoSecret = "midjourney-image-handler-test-secret"
	constant.MaxFileDownloadMB = 1
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = true
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = []string{"1-65535"}
	fetchSetting.ApplyIPFilterForDomain = true
	pngBody := []byte{'\x89', 'P', 'N', 'G', '\r', '\n', '\x1a', '\n', 0, 0, 0, 0}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/image":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBody)
		case "/html":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("<html><script>alert(1)</script></html>"))
		case "/large":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	tasks := []*model.Midjourney{
		{UserId: 7, MjId: "task-image", ImageUrl: upstream.URL + "/image"},
		{UserId: 7, MjId: "task-html", ImageUrl: upstream.URL + "/html"},
		{UserId: 7, MjId: "task-large", ImageUrl: upstream.URL + "/large"},
	}
	for _, task := range tasks {
		require.NoError(t, db.Create(task).Error)
	}

	now := time.Now()
	validExpires := now.Add(midjourneyImageURLTTL).Unix()
	validQuery := func(taskID string, userID int, expires int64) string {
		query := url.Values{}
		query.Set("user_id", strconv.Itoa(userID))
		query.Set("expires", strconv.FormatInt(expires, 10))
		query.Set("signature", midjourneyImageSignature(userID, taskID, expires))
		return query.Encode()
	}
	serve := func(taskID, rawQuery string, requestContext context.Context) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		target := "/mj/image/" + taskID
		if rawQuery != "" {
			target += "?" + rawQuery
		}
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if requestContext != nil {
			request = request.WithContext(requestContext)
		}
		c.Request = request
		c.Params = gin.Params{{Key: "id", Value: taskID}}
		RelayMidjourneyImage(c)
		return w
	}

	t.Run("legacy unsigned URL is rejected", func(t *testing.T) {
		response := serve("task-image", "", nil)
		assert.Equal(t, http.StatusForbidden, response.Code)
	})

	t.Run("expired URL is rejected", func(t *testing.T) {
		expires := time.Now().Add(-time.Second).Unix()
		response := serve("task-image", validQuery("task-image", 7, expires), nil)
		assert.Equal(t, http.StatusForbidden, response.Code)
	})

	t.Run("expiry beyond 24 hours is rejected", func(t *testing.T) {
		expires := time.Now().Add(midjourneyImageURLTTL + time.Minute).Unix()
		response := serve("task-image", validQuery("task-image", 7, expires), nil)
		assert.Equal(t, http.StatusForbidden, response.Code)
	})

	t.Run("tampered user is rejected", func(t *testing.T) {
		query, err := url.ParseQuery(validQuery("task-image", 7, validExpires))
		require.NoError(t, err)
		query.Set("user_id", "8")
		response := serve("task-image", query.Encode(), nil)
		assert.Equal(t, http.StatusForbidden, response.Code)
	})

	t.Run("valid signature for another user cannot read the task", func(t *testing.T) {
		response := serve("task-image", validQuery("task-image", 8, validExpires), nil)
		assert.Equal(t, http.StatusNotFound, response.Code)
	})

	t.Run("valid URL returns only an image with private headers", func(t *testing.T) {
		response := serve("task-image", validQuery("task-image", 7, validExpires), nil)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "image/png", response.Header().Get("Content-Type"))
		assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
		assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, pngBody, response.Body.Bytes())
		assert.Empty(t, response.Header().Values("Set-Cookie"))
	})

	t.Run("active content is rejected despite forged image header", func(t *testing.T) {
		response := serve("task-html", validQuery("task-html", 7, validExpires), nil)
		assert.Equal(t, http.StatusUnsupportedMediaType, response.Code)
	})

	t.Run("oversized image is rejected", func(t *testing.T) {
		response := serve("task-large", validQuery("task-large", 7, validExpires), nil)
		assert.Equal(t, http.StatusBadGateway, response.Code)
	})

	t.Run("downstream cancellation reaches image fetch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		response := serve("task-image", validQuery("task-image", 7, validExpires), ctx)
		assert.Equal(t, http.StatusBadGateway, response.Code)
	})
}

func TestCoverMidjourneyTaskDtoReturnsFreshSignedImageURL(t *testing.T) {
	oldSecret := common.CryptoSecret
	oldServerAddress := system_setting.ServerAddress
	oldForward := setting.MjForwardUrlEnabled
	common.CryptoSecret = "midjourney-cover-test-secret"
	system_setting.ServerAddress = "https://newapi.example"
	setting.MjForwardUrlEnabled = true
	t.Cleanup(func() {
		common.CryptoSecret = oldSecret
		system_setting.ServerAddress = oldServerAddress
		setting.MjForwardUrlEnabled = oldForward
	})

	origin := &model.Midjourney{UserId: 9, MjId: "mj-cover-task", ImageUrl: "https://images.example/result.png"}
	covered := coverMidjourneyTaskDto(nil, origin)
	parsed, err := url.Parse(covered.ImageUrl)
	require.NoError(t, err)
	expires, err := strconv.ParseInt(parsed.Query().Get("expires"), 10, 64)
	require.NoError(t, err)
	assert.Equal(t, "9", parsed.Query().Get("user_id"))
	assert.True(t, expires > time.Now().Unix())
	assert.True(t, validMidjourneyImageSignature(9, origin.MjId, expires, parsed.Query().Get("signature")))
}
