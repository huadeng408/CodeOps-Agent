package handler

import (
	"code-agent/internal/session"
	"github.com/gin-gonic/gin"
	"net/http"
)

type TaskWorkspaceHandler struct {
	workbench session.WorkbenchModule
	tasks     session.TaskWorkspaceModule
}

func NewTaskWorkspaceHandler(w session.WorkbenchModule, tasks session.TaskWorkspaceModule) *TaskWorkspaceHandler {
	return &TaskWorkspaceHandler{workbench: w, tasks: tasks}
}

func (h *TaskWorkspaceHandler) Inspect(c *gin.Context) { h.handle(c, false) }
func (h *TaskWorkspaceHandler) Prepare(c *gin.Context) { h.handle(c, true) }

func (h *TaskWorkspaceHandler) handle(c *gin.Context, prepare bool) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authenticated owner is required")
		return
	}
	if h.tasks == nil {
		view, err := h.workbench.Get(c.Request.Context(), owner, c.Param("id"))
		if err != nil {
			writeSessionError(c, err, "session unavailable")
			return
		}
		if prepare {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "task repository and storage are not approved"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "data": session.TaskWorkspaceView{State: "blocked", Reason: "task repository and storage are not approved", Repository: view.ProjectName, EventCount: view.EventCount}})
		return
	}
	var view session.TaskWorkspaceView
	if prepare {
		var request struct {
			ExpectedSeq *int64 `json:"expectedSeq"`
			RequestID   string `json:"requestId"`
		}
		if c.ShouldBindJSON(&request) != nil || request.ExpectedSeq == nil {
			writeSessionError(c, session.ErrInvalidSessionInput, "invalid workspace request")
			return
		}
		view, err = h.tasks.Prepare(c.Request.Context(), owner, c.Param("id"), *request.ExpectedSeq, request.RequestID)
	} else {
		view, err = h.tasks.Inspect(c.Request.Context(), owner, c.Param("id"))
	}
	if err != nil {
		writeSessionError(c, err, "task workspace unavailable", h.workbench)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": view})
}
