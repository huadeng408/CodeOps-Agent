package middleware

import (
	"net/http"
	"strings"

	"code-agent/internal/serverconfig"

	"github.com/gin-gonic/gin"
)

// InternalAuthMiddleware protects internal-only endpoints used by the external orchestrator service.
func InternalAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := strings.TrimSpace(serverconfig.Conf.AI.Orchestrator.SharedSecret)
		if expected == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "internal orchestrator secret is not configured"})
			return
		}
		if c.GetHeader("X-Internal-Token") != expected {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid internal token"})
			return
		}
		c.Next()
	}
}
