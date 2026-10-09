package middleware

import (
	"io"
	"net/http"

	"code-agent/pkg/log"

	"github.com/gin-gonic/gin"
)

// Gin's default request dump retains Cookie and arbitrary credential headers.
func RedactedRecovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		log.Errorf("HTTP request panic recovered")
		c.AbortWithStatus(http.StatusInternalServerError)
	})
}
