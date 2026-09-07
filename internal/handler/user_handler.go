// Package handler 包含了处理 HTTP 请求的控制器逻辑。
package handler

import (
	"code-agent/internal/service"
	"code-agent/pkg/log"
	"code-agent/pkg/token"
	"net/http"
	"time"

	"code-agent/internal/model"
	"github.com/gin-gonic/gin"
)

// UserHandler 负责处理所有与普通用户相关的 API 请求。
type UserHandler struct {
	userService service.UserService
}

// NewUserHandler 创建一个新的 UserHandler 实例。
func NewUserHandler(userService service.UserService) *UserHandler {
	return &UserHandler{userService: userService}
}

// RegisterRequest 定义了用户注册 API 的请求体结构。
type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
	Name     string `json:"name"`
}

// Register 处理用户注册请求。
func (h *UserHandler) Register(c *gin.Context) {
	var req RegisterRequest
	// 绑定并验证 JSON 请求体
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warnf("Register: Invalid request payload, error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    http.StatusBadRequest,
			"message": "无效的请求负载：邮箱和密码不能为空",
		})
		return
	}

	// Use email as username if username is not provided
	username := req.Username
	if username == "" {
		username = req.Email
	}

	// 调用 service 层执行注册逻辑
	user, err := h.userService.Register(username, req.Password)
	if err != nil {
		log.Warnf("Register: User registration failed for '%s', error: %v", username, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	log.Infof("User '%s' registered successfully", user.Username)
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "User registered successfully",
	})
}

// LoginRequest 定义了用户登录 API 的请求体结构。
type LoginRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password" binding:"required"`
}

// Login 处理用户登录请求。
func (h *UserHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warnf("Login: Invalid request payload, error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    http.StatusBadRequest,
			"message": "无效的请求负载：邮箱/用户名和密码不能为空",
		})
		return
	}

	// Use email as username if username is not provided
	username := req.Username
	if username == "" {
		username = req.Email
	}

	// 调用 service 层执行登录逻辑
	accessToken, refreshToken, err := h.userService.Login(username, req.Password)
	if err != nil {
		log.Warnf("Login: User authentication failed for '%s', error: %v", req.Username, err)
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    http.StatusUnauthorized,
			"message": "无效的凭证",
		})
		return
	}

	log.Infof("User '%s' logged in successfully", req.Username)
	// Browser clients authenticate with HttpOnly cookies; the JSON fields stay
	// temporarily for existing non-browser clients during migration.
	token.SetAuthCookies(c.Writer, c.Request, accessToken, refreshToken, 24*60*60, 7*24*60*60)
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "Login successful",
		"data": gin.H{
			"token":        accessToken,
			"refreshToken": refreshToken,
		},
	})
}

// ProfileResponse 定义了获取用户个人信息 API 的响应体结构。
type ProfileResponse struct {
	ID         uint      `json:"id"`
	Username   string    `json:"username"`
	Role       string    `json:"role"`
	OrgTags    []string  `json:"orgTags"`
	PrimaryOrg string    `json:"primaryOrg"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// GetProfile 获取当前登录用户的个人信息。
// 用户信息已经由 AuthMiddleware 注入到上下文中。
func (h *UserHandler) GetProfile(c *gin.Context) {
	// 从上下文中获取由 AuthMiddleware 注入的 User 对象
	user, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法获取用户信息"})
		return
	}

	// 直接返回用户信息
	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": user, "message": "success"})
}

// Logout 处理用户登出逻辑。
func (h *UserHandler) Logout(c *gin.Context) {
	tokenString, tokenErr := token.AccessTokenFromRequest(c.Request)
	if tokenErr != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": http.StatusUnauthorized, "message": "未认证"})
		return
	}
	refreshToken, refreshErr := token.RefreshTokenFromRequest(c.Request)
	if refreshErr != nil {
		refreshToken = ""
	}

	err := h.userService.Logout(tokenString, refreshToken)
	if err != nil {
		log.Error("Logout: Failed to logout", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    http.StatusInternalServerError,
			"message": "登出失败",
		})
		return
	}
	username := ""
	if userValue, exists := c.Get("user"); exists {
		if user, ok := userValue.(*model.User); ok && user != nil {
			username = user.Username
		}
	}
	log.Infof("User '%s' logged out successfully", username)
	token.ClearAuthCookies(c.Writer, c.Request)
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "登出成功"})
}

// SetPrimaryOrgRequest 定义了设置主组织 API 的请求体结构。
type SetPrimaryOrgRequest struct {
	PrimaryOrg string `json:"primaryOrg" binding:"required"`
}

// SetPrimaryOrg 处理设置用户主组织的请求。
func (h *UserHandler) SetPrimaryOrg(c *gin.Context) {
	var req SetPrimaryOrgRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warnf("SetPrimaryOrg: Invalid request payload, error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    http.StatusBadRequest,
			"message": "无效的请求负载",
		})
		return
	}

	// 优先从上下文中获取完整的用户对象（由 AuthMiddleware 注入）
	userValue, ok := c.Get("user")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    http.StatusUnauthorized,
			"message": "未认证用户或无法获取用户信息",
		})
		return
	}
	user, ok := userValue.(*model.User)
	if !ok || user == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    http.StatusInternalServerError,
			"message": "用户数据类型错误",
		})
		return
	}
	err := h.userService.SetUserPrimaryOrg(user.Username, req.PrimaryOrg)
	if err != nil {
		log.Warnf("SetPrimaryOrg: Failed for user '%s', error: %v", user.Username, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    http.StatusInternalServerError,
			"message": err.Error(),
		})
		return
	}
	log.Infof("User '%s' set primary organization to '%s'", user.Username, req.PrimaryOrg)
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "主组织更新成功",
	})
}

// GetUserOrgTags 获取用户的组织标签信息。
func (h *UserHandler) GetUserOrgTags(c *gin.Context) {
	// 从上下文中获取由 AuthMiddleware 注入的 User 对象
	userValue, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法获取用户信息"})
		return
	}

	user, ok := userValue.(*model.User)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "用户数据类型错误"})
		return
	}

	tags, err := h.userService.GetUserOrgTags(user.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": http.StatusOK, "data": tags, "message": "success"})
}
