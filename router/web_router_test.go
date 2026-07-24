package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrontendNoRouteHandlerRejectsMissingStaticAsset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.NoRoute(frontendNoRouteHandler(WebAssets{
		IndexPage: []byte("<!doctype html><div id=\"root\"></div>"),
	}))

	request := httptest.NewRequest(http.MethodGet, "/static/js/async/OLD_HASH.js", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.NotContains(t, response.Header().Get("Content-Type"), "text/html")
	assert.NotContains(t, response.Body.String(), "<!doctype html>")
}

func TestFrontendNoRouteHandlerKeepsSpaFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	indexPage := []byte("<!doctype html><div id=\"root\"></div>")
	engine.NoRoute(frontendNoRouteHandler(WebAssets{
		IndexPage: indexPage,
	}))

	request := httptest.NewRequest(http.MethodGet, "/dashboard/channels", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
	assert.Contains(t, response.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, string(indexPage), response.Body.String())
}
