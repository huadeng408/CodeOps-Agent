// Package handler 包含了处理 HTTP 请求的控制器逻辑。
package handler

import (
	"code-agent/internal/service"
	"code-agent/pkg/log"
	"code-agent/pkg/token"
	"github.com/gin-gonic/gin"
	"net/http"
)

// AuthHandler 负责处理认证相关的 API 请求，例如刷新 token。
type AuthHandler struct {
	userService service.UserService
}

// NewAuthHandler 创建一个新的 AuthHandler 实例。
func NewAuthHandler(userService service.UserService) *AuthHandler {
	return &AuthHandler{userService: userService}
}

// RefreshTokenRequest 定义了刷新 token API 的请求体结构。
type RefreshTokenRequest struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

// RefreshToken 处理刷新 token 的请求。
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req RefreshTokenRequest
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		// JSON is optional for browser clients, which keep the refresh token in
		// an HttpOnly cookie. A malformed body is handled as a missing token.
		_ = c.ShouldBindJSON(&req)
	}
	if req.RefreshToken == "" {
		if cookieToken, err := token.RefreshTokenFromRequest(c.Request); err == nil {
			req.RefreshToken = cookieToken
		}
	}
	if req.RefreshToken == "" {
		log.Warnf("RefreshToken: Invalid request payload: refreshToken is missing")
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求负载：refreshToken 不能为空"})
		return
	}

	newAccessToken, newRefreshToken, err := h.userService.RefreshToken(req.RefreshToken)
	if err != nil {
		log.Warnf("RefreshToken: Failed to refresh token, error: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "无效的 refresh token"})
		return
	}

	log.Info("Token refreshed successfully")
	token.SetAuthCookies(c.Writer, c.Request, newAccessToken, newRefreshToken, 24*60*60, 7*24*60*60)
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "Token refreshed successfully",
		"data": gin.H{
			"token":        newAccessToken,
			"refreshToken": newRefreshToken,
		},
	})
}
