package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTurnstileTest(t *testing.T, verifyHandler http.HandlerFunc) (*gin.Engine, gin.HandlerFunc) {
	t.Helper()

	previousEnabled := common.TurnstileCheckEnabled
	previousSecret := common.TurnstileSecretKey
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"

	verifyServer := httptest.NewServer(verifyHandler)

	t.Cleanup(func() {
		verifyServer.Close()
		common.TurnstileCheckEnabled = previousEnabled
		common.TurnstileSecretKey = previousSecret
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	store := cookie.NewStore([]byte("turnstile-test-session-secret"))
	router.Use(sessions.Sessions("session", store))
	return router, turnstileCheckWithClient(verifyServer.Client(), verifyServer.URL)
}

func TestTurnstileCheckIgnoresAndDeletesLegacySessionGrant(t *testing.T) {
	var verificationCount atomic.Int32
	router, turnstileCheck := setupTurnstileTest(t, func(w http.ResponseWriter, _ *http.Request) {
		verificationCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})

	router.GET("/seed", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("turnstile", true)
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	handlerCalled := false
	router.GET("/protected", turnstileCheck, func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})
	router.GET("/inspect", func(c *gin.Context) {
		assert.Nil(t, sessions.Default(c).Get("turnstile"))
		c.Status(http.StatusNoContent)
	})

	seedResponse := httptest.NewRecorder()
	router.ServeHTTP(seedResponse, httptest.NewRequest(http.MethodGet, "/seed", nil))
	require.NotEmpty(t, seedResponse.Result().Cookies())

	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(seedResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.False(t, handlerCalled)
	assert.Contains(t, response.Body.String(), "Turnstile token")
	assert.EqualValues(t, 0, verificationCount.Load())
	require.NotEmpty(t, response.Result().Cookies())

	inspectRequest := httptest.NewRequest(http.MethodGet, "/inspect", nil)
	inspectRequest.AddCookie(response.Result().Cookies()[0])
	inspectResponse := httptest.NewRecorder()
	router.ServeHTTP(inspectResponse, inspectRequest)
	assert.Equal(t, http.StatusNoContent, inspectResponse.Code)
}

func TestTurnstileCheckDoesNotPersistSuccessfulVerification(t *testing.T) {
	var verificationCount atomic.Int32
	requests := make(chan [3]string, 1)
	router, turnstileCheck := setupTurnstileTest(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- [3]string{r.Form.Get("secret"), r.Form.Get("response"), r.Form.Get("remoteip")}
		verificationCount.Add(1)
		_, _ = fmt.Fprint(w, `{"success":true}`)
	})

	router.GET("/protected", turnstileCheck, func(c *gin.Context) {
		assert.Nil(t, sessions.Default(c).Get("turnstile"))
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/protected?turnstile=fresh-token", nil))

	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"success":true}`, response.Body.String())
	assert.EqualValues(t, 1, verificationCount.Load())
	assert.Empty(t, response.Result().Cookies())
	requestForm := <-requests
	assert.Equal(t, "test-secret", requestForm[0])
	assert.Equal(t, "fresh-token", requestForm[1])
	assert.NotEmpty(t, requestForm[2])
}

func TestTurnstileCheckRevalidatesEveryProtectedRequest(t *testing.T) {
	var verificationCount atomic.Int32
	router, turnstileCheck := setupTurnstileTest(t, func(w http.ResponseWriter, _ *http.Request) {
		attempt := verificationCount.Add(1)
		_, _ = fmt.Fprintf(w, `{"success":%t}`, attempt == 1)
	})

	var handlerCount atomic.Int32
	router.GET("/protected", turnstileCheck, func(c *gin.Context) {
		handlerCount.Add(1)
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	firstResponse := httptest.NewRecorder()
	router.ServeHTTP(firstResponse, httptest.NewRequest(http.MethodGet, "/protected?turnstile=first", nil))
	secondResponse := httptest.NewRecorder()
	router.ServeHTTP(secondResponse, httptest.NewRequest(http.MethodGet, "/protected?turnstile=replayed", nil))

	assert.JSONEq(t, `{"success":true}`, firstResponse.Body.String())
	assert.JSONEq(t, `{"message":"Turnstile 校验失败，请刷新重试！","success":false}`, secondResponse.Body.String())
	assert.EqualValues(t, 2, verificationCount.Load())
	assert.EqualValues(t, 1, handlerCount.Load())
}

func TestTurnstileCheckRejectsNonSuccessHTTPStatus(t *testing.T) {
	router, turnstileCheck := setupTurnstileTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, `{"success":true}`)
	})

	handlerCalled := false
	router.GET("/protected", turnstileCheck, func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/protected?turnstile=fresh-token", nil))

	assert.False(t, handlerCalled)
	assert.JSONEq(t, `{"message":"Turnstile 校验失败，请刷新重试！","success":false}`, response.Body.String())
}

func TestTurnstileCheckRejectsMalformedResponseWithoutLeakingParserError(t *testing.T) {
	router, turnstileCheck := setupTurnstileTest(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{not-json`)
	})

	router.GET("/protected", turnstileCheck, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/protected?turnstile=fresh-token", nil))

	assert.JSONEq(t, `{"message":"Turnstile 校验失败，请刷新重试！","success":false}`, response.Body.String())
	assert.NotContains(t, response.Body.String(), "invalid character")
}

func TestTurnstileCheckUsesRequestContextWithoutLeakingCancellationError(t *testing.T) {
	router, turnstileCheck := setupTurnstileTest(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"success":true}`)
	})

	handlerCalled := false
	router.GET("/protected", turnstileCheck, func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/protected?turnstile=fresh-token", nil).WithContext(requestContext)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.False(t, handlerCalled)
	assert.JSONEq(t, `{"message":"Turnstile 校验失败，请刷新重试！","success":false}`, response.Body.String())
	assert.NotContains(t, response.Body.String(), "context canceled")
}
