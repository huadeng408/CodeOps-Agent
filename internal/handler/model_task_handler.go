package handler

import (
	"net/http"

	"code-agent/internal/admission"
	"code-agent/internal/session"
	"github.com/gin-gonic/gin"
)

// ModelTaskHandler leaves completion and accounting in the Go admission module.
type ModelTaskHandler struct {
	workbench session.WorkbenchModule
	gateway   *admission.Gateway
}

func NewModelTaskHandler(workbench session.WorkbenchModule, gateway *admission.Gateway) *ModelTaskHandler {
	return &ModelTaskHandler{workbench: workbench, gateway: gateway}
}

func (h *ModelTaskHandler) Finish(c *gin.Context) {
	h.closeAdmission(c, false)
}

func (h *ModelTaskHandler) Abandon(c *gin.Context) {
	h.closeAdmission(c, true)
}

func (h *ModelTaskHandler) closeAdmission(c *gin.Context, abandon bool) {
	owner, err := authenticatedOwner(c)
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	if _, err := h.workbench.Get(c.Request.Context(), owner, c.Param("id")); err != nil {
		writeSessionError(c, err, "task is unavailable")
		return
	}
	if h.gateway == nil {
		writeSessionError(c, session.ErrContinuationUnavailable, "task admission is unavailable")
		return
	}
	actor, err := authenticatedActor(c, c.Param("id"))
	if err != nil {
		writeSessionError(c, err, "authentication required")
		return
	}
	if abandon {
		err = h.gateway.AbandonCanceledTask(c.Request.Context(), actor)
	} else {
		err = h.gateway.FinishTask(c.Request.Context(), actor)
	}
	if err != nil {
		writeSessionError(c, err, "task completion is unavailable", h.workbench)
		return
	}
	data := gin.H{"sessionId": c.Param("id")}
	if abandon {
		data["admissionState"] = "abandoned"
	}
	writeSessionData(c, http.StatusOK, data)
}
