// Package handler 包含了处理 HTTP 请求的控制器逻辑。
package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"code-agent/internal/service"
	"code-agent/pkg/token"
	"time"
)

// ConversationHandler 处理与对话相关的 API 请求。
type ConversationHandler struct {
	service service.ConversationService
}

// NewConversationHandler 创建一个新的 ConversationHandler。
func NewConversationHandler(service service.ConversationService) *ConversationHandler {
	return &ConversationHandler{service: service}
}

// GetConversations 处理获取用户对话历史的请求。
func (h *ConversationHandler) GetConversations(c *gin.Context) {
	claims := c.MustGet("claims").(*token.CustomClaims)

	var startTime, endTime *time.Time
	const timeLayout = "2006-01-02"
	if startDateStr := c.Query("start_date"); startDateStr != "" {
		t, err := time.Parse(timeLayout, startDateStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    http.StatusBadRequest,
				"message": "Invalid start_date format, use YYYY-MM-DD",
				"data":    nil,
			})
			return
		}
		startTime = &t
	}
	if endDateStr := c.Query("end_date"); endDateStr != "" {
		t, err := time.Parse(timeLayout, endDateStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    http.StatusBadRequest,
				"message": "Invalid end_date format, use YYYY-MM-DD",
				"data":    nil,
			})
			return
		}
		t = t.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
		endTime = &t
	}

	history, err := h.service.GetConversationHistory(c.Request.Context(), claims.UserID, startTime, endTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    http.StatusInternalServerError,
			"message": "Failed to retrieve conversation history",
			"data":    nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "success",
		"data":    history,
	})
}
