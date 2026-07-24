package middleware

import (
	"fmt"
	"io"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

// accessLogWriter resolves Gin's current writer for every log entry. Gin's
// built-in logger otherwise captures gin.DefaultWriter when the middleware is
// installed, which would keep writing to a closed file after log rotation.
type accessLogWriter struct{}

func (accessLogWriter) Write(p []byte) (int, error) {
	common.LogWriterMu.RLock()
	defer common.LogWriterMu.RUnlock()
	return gin.DefaultWriter.Write(p)
}

type errorLogWriter struct{}

func (errorLogWriter) Write(p []byte) (int, error) {
	common.LogWriterMu.RLock()
	defer common.LogWriterMu.RUnlock()
	return gin.DefaultErrorWriter.Write(p)
}

func CurrentGinErrorWriter() io.Writer {
	return errorLogWriter{}
}

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		Output: accessLogWriter{},
		Formatter: func(param gin.LogFormatterParams) string {
			var requestID string
			if param.Keys != nil {
				requestID, _ = param.Keys[common.RequestIdKey].(string)
			}
			tag, _ := param.Keys[RouteTagKey].(string)
			if tag == "" {
				tag = "web"
			}
			// gin's formatter Path may include URL.RawQuery. Some compatible API
			// endpoints accept credentials in query parameters, so only log the
			// escaped path component here.
			path := "/"
			if param.Request != nil && param.Request.URL != nil {
				if escapedPath := param.Request.URL.EscapedPath(); escapedPath != "" {
					path = escapedPath
				}
			}
			return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
				param.TimeStamp.Format("2006/01/02 - 15:04:05"),
				tag,
				requestID,
				param.StatusCode,
				param.Latency,
				param.ClientIP,
				param.Method,
				path,
			)
		},
	}))
}
