package handler

import (
	"errors"
	"net/http"
	"strings"

	"code-agent/internal/session"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var errAuthenticatedOwner = errors.New("authenticated owner is required")

func authenticatedOwner(c *gin.Context) (uint, error) {
	value, ok := c.Get("claims")
	if !ok {
		return 0, errAuthenticatedOwner
	}
	claims, ok := value.(*token.CustomClaims)
	if !ok || claims == nil || claims.UserID == 0 {
		return 0, errAuthenticatedOwner
	}
	return claims.UserID, nil
}

func sessionErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, errAuthenticatedOwner) || errors.Is(err, session.ErrSessionOwnerRequired) {
		return http.StatusUnauthorized
	}
	if errors.Is(err, session.ErrSessionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, session.ErrSequenceConflict) || errors.Is(err, session.ErrSessionStateConflict) {
		return http.StatusConflict
	}
	if errors.Is(err, session.ErrInvalidSessionInput) {
		return http.StatusBadRequest
	}
	if errors.Is(err, session.ErrContinuationUnavailable) || errors.Is(err, session.ErrSessionRunnerClosed) {
		return http.StatusServiceUnavailable
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "not found") || strings.Contains(message, "does not belong") {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func writeSessionError(c *gin.Context, err error, fallback string, workbenches ...session.WorkbenchModule) {
	status := sessionErrorStatus(err)
	message := fallback
	if errors.Is(err, session.ErrContinuationUnavailable) || errors.Is(err, session.ErrSessionRunnerClosed) {
		message = "agent continuation is unavailable"
	} else if status == http.StatusBadRequest || status == http.StatusConflict {
		message = err.Error()
	}
	body := gin.H{"code": status, "message": message, "data": nil}
	if status == http.StatusConflict && c.Param("id") != "" {
		// A conflict is a stale client cursor, so return the authoritative
		// cursor alongside the error. The browser can refresh and retry a
		// non-destructive user message without guessing.
		if len(workbenches) > 0 {
			if owner, ownerErr := authenticatedOwner(c); ownerErr == nil && workbenches[0] != nil {
				if view, viewErr := workbenches[0].Get(c.Request.Context(), owner, c.Param("id")); viewErr == nil {
					body["cursor"] = gin.H{"sessionId": view.ID, "eventCount": view.EventCount}
				}
			}
		}
	}
	c.JSON(status, body)
}
