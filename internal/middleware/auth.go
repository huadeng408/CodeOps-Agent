// Package middleware 提供了处理 HTTP 请求的中间件。
package middleware

import (
	"code-agent/internal/service"
	"code-agent/pkg/token"
	"net/http"

	"github.com/gin-gonic/gin"
)

const accessTokenCookieName = token.AccessTokenCookieName

// AuthMiddleware 创建一个 Gin 中间件，用于 JWT 认证。
// 它会从请求头中提取 token，验证其有效性，并将完整的 User 对象存入 Gin 的上下文中。
func AuthMiddleware(jwtManager *token.JWTManager, userService service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, extractErr := token.AccessTokenFromRequest(c.Request)
		if extractErr != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请求未包含有效的授权凭据"})
			return
		}
		isBlacklisted, err := userService.IsTokenBlacklisted(tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "token blacklist check failed"})
			return
		}
		if isBlacklisted {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token has been revoked"})
			return
		}

		claims, err := jwtManager.VerifyToken(tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "无效或已过期的 token"})
			return
		}
		if claims.TokenType != token.TokenTypeAccess {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "无效的 token 类型"})
			return
		}

		// 使用 claims 中的用户名从数据库获取完整的用户信息
		user, err := userService.GetProfile(claims.Username)
		if err != nil {
			// 如果根据 token 中的用户信息无法找到用户，说明该用户可能已被删除
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "用户不存在"})
			return
		}

		// 将完整的 User 对象存储在 context 中，供后续处理函数使用
		c.Set("user", user)

		// 为了向后兼容或特殊用途，仍然可以存储 claims
		c.Set("claims", claims)

		// 继续处理请求链中的下一个处理器
		c.Next()
	}
}
