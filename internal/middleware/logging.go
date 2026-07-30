// Package middleware 存放 Gin 框架的中间件。
package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"code-agent/pkg/log"

	"github.com/gin-gonic/gin"
)

const redactedLogValue = "[REDACTED]"

// bodyLogWriter 用于捕获响应体
type bodyLogWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

// Write 实现了 io.Writer 接口，将响应写入 gin.ResponseWriter 和一个内部的 buffer
func (w bodyLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// RequestLogger 是一个 Gin 中间件，用于记录详细的请求和响应日志。
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 记录请求开始时间
		startTime := time.Now()

		// 读取并重新缓存请求体
		var requestBody []byte
		if c.Request.Body != nil {
			requestBody, _ = io.ReadAll(c.Request.Body)
		}
		// 将读取的请求体重新设置回 c.Request.Body，以便后续处理函数可以正常读取
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

		// 使用自定义的 ResponseWriter 捕获响应
		blw := &bodyLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = blw

		// 处理请求
		c.Next()

		// 计算延迟
		latency := time.Since(startTime)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		path := c.Request.URL.Path

		// 记录完整的请求和响应信息
		log.Infow("HTTP Request Log",
			"statusCode", statusCode,
			"latency", latency.String(),
			"clientIP", clientIP,
			"method", method,
			"path", path,
			"requestBody", redactJSONLogBody(requestBody),
			"responseBody", redactJSONLogBody(blw.body.Bytes()),
		)
	}
}

func redactJSONLogBody(body []byte) string {
	var value any
	if len(body) == 0 || json.Unmarshal(body, &value) != nil {
		return string(body)
	}
	redactSensitiveJSONFields(value)
	redacted, err := json.Marshal(value)
	if err != nil {
		return string(body)
	}
	return string(redacted)
}

func redactSensitiveJSONFields(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isSensitiveLogKey(key) {
				typed[key] = redactedLogValue
				continue
			}
			redactSensitiveJSONFields(child)
		}
	case []any:
		for _, child := range typed {
			redactSensitiveJSONFields(child)
		}
	}
}

func isSensitiveLogKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(strings.ToLower(key))
	for _, suffix := range []string{"password", "token", "secret", "apikey", "authorization", "cookie"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
