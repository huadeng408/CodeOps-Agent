package handler

import (
	"bytes"
	"code-agent/internal/session"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
)

type TaskWorkspaceHandler struct {
	workbench session.WorkbenchModule
	tasks     session.TaskWorkspaceModule
}

func NewTaskWorkspaceHandler(w session.WorkbenchModule, tasks session.TaskWorkspaceModule) *TaskWorkspaceHandler {
	return &TaskWorkspaceHandler{workbench: w, tasks: tasks}
}

func (h *TaskWorkspaceHandler) Inspect(c *gin.Context)     { h.handle(c, "") }
func (h *TaskWorkspaceHandler) Prepare(c *gin.Context)     { h.handle(c, "prepare") }
func (h *TaskWorkspaceHandler) RunBaseline(c *gin.Context) { h.handle(c, "test") }

func (h *TaskWorkspaceHandler) handle(c *gin.Context, action string) {
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
		if action != "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "task repository and storage are not approved"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "data": session.TaskWorkspaceView{State: "blocked", Reason: "task repository and storage are not approved", Repository: view.ProjectName, EventCount: view.EventCount}})
		return
	}
	var view session.TaskWorkspaceView
	if action != "" {
		var request struct {
			ExpectedSeq *int64 `json:"expectedSeq"`
			RequestID   string `json:"requestId"`
		}
		data, readErr := io.ReadAll(io.LimitReader(c.Request.Body, 4097))
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if readErr != nil || len(data) > 4096 || decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.ExpectedSeq == nil {
			writeSessionError(c, session.ErrInvalidSessionInput, "invalid workspace request")
			return
		}
		if action == "test" {
			view, err = h.tasks.RunBaseline(c.Request.Context(), owner, c.Param("id"), *request.ExpectedSeq, request.RequestID)
		} else {
			view, err = h.tasks.Prepare(c.Request.Context(), owner, c.Param("id"), *request.ExpectedSeq, request.RequestID)
		}
	} else {
		view, err = h.tasks.Inspect(c.Request.Context(), owner, c.Param("id"))
	}
	if err != nil {
		writeSessionError(c, err, "task workspace unavailable", h.workbench)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": view})
}
