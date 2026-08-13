// Package middleware contains Gin framework middleware.
package middleware

import (
	"time"

	"code-agent/pkg/log"

	"github.com/gin-gonic/gin"
)

// responseSizeWriter records response size without retaining response content.
// Access logging must never become a second store for prompts or retrieved text.
type responseSizeWriter struct {
	gin.ResponseWriter
	bytes int64
}

func (w *responseSizeWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func (w *responseSizeWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	w.bytes += int64(n)
	return n, err
}

// RequestLogger records request metadata only. Request and response bodies can
// contain credentials, prompts, user data, and retrieved document content.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()
		responseWriter := &responseSizeWriter{ResponseWriter: c.Writer}
		c.Writer = responseWriter

		c.Next()

		log.Infow("HTTP Request Log",
			"statusCode", c.Writer.Status(),
			"latency", time.Since(startTime).String(),
			"clientIP", c.ClientIP(),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"requestBytes", max(c.Request.ContentLength, 0),
			"responseBytes", responseWriter.bytes,
		)
	}
}
