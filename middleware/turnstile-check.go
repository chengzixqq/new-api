package middleware

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

type turnstileCheckResponse struct {
	Success bool `json:"success"`
}

const (
	turnstileVerifyEndpoint  = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	turnstileMaxResponseSize = 64 * 1024
)

func TurnstileCheck() gin.HandlerFunc {
	return turnstileCheckWithClient(&http.Client{Timeout: 10 * time.Second}, turnstileVerifyEndpoint)
}

func turnstileCheckWithClient(httpClient *http.Client, verifyEndpoint string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.TurnstileCheckEnabled {
			session := sessions.Default(c)
			// Older versions stored a successful Turnstile result in the signed
			// session cookie. Every protected action must verify a fresh token.
			if session.Get("turnstile") != nil {
				session.Delete("turnstile")
				if err := session.Save(); err != nil {
					c.JSON(http.StatusOK, gin.H{
						"message": "无法保存会话信息，请重试",
						"success": false,
					})
					c.Abort()
					return
				}
			}

			response := c.Query("turnstile")
			if response == "" {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile token 为空",
				})
				c.Abort()
				return
			}
			form := url.Values{
				"secret":   {common.TurnstileSecretKey},
				"response": {response},
				"remoteip": {c.ClientIP()},
			}
			request, err := http.NewRequestWithContext(
				c.Request.Context(),
				http.MethodPost,
				verifyEndpoint,
				strings.NewReader(form.Encode()),
			)
			if err != nil {
				common.SysLog(err.Error())
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验失败，请刷新重试！",
				})
				c.Abort()
				return
			}
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			rawRes, err := httpClient.Do(request)
			if err != nil {
				common.SysLog(err.Error())
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验失败，请刷新重试！",
				})
				c.Abort()
				return
			}
			defer rawRes.Body.Close()
			if rawRes.StatusCode < http.StatusOK || rawRes.StatusCode >= http.StatusMultipleChoices {
				common.SysLog("Turnstile siteverify returned " + rawRes.Status)
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验失败，请刷新重试！",
				})
				c.Abort()
				return
			}

			body, err := io.ReadAll(io.LimitReader(rawRes.Body, turnstileMaxResponseSize+1))
			if err != nil || len(body) > turnstileMaxResponseSize {
				if err != nil {
					common.SysLog(err.Error())
				} else {
					common.SysLog("Turnstile siteverify response exceeded size limit")
				}
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验失败，请刷新重试！",
				})
				c.Abort()
				return
			}

			var res turnstileCheckResponse
			err = common.Unmarshal(body, &res)
			if err != nil {
				common.SysLog(err.Error())
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验失败，请刷新重试！",
				})
				c.Abort()
				return
			}
			if !res.Success {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "Turnstile 校验失败，请刷新重试！",
				})
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
