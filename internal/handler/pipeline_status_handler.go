package handler

import (
	"net/http"
	"regexp"
	"strings"

	"code-agent/internal/model"

	"github.com/gin-gonic/gin"
)

var pipelineRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,95}$`)
var pipelineFileMD5Pattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

var pipelineStages = []string{"parse", "chunk", "embed", "index"}

// PipelineRunTaskLister is the read-only query used by the integration gate.
type PipelineRunTaskLister interface {
	ListByRunAndFile(runID, fileMD5 string) ([]model.PipelineTask, error)
}

// PipelineStatusHandler reports stage state for one exact controlled run/file.
type PipelineStatusHandler struct {
	repo PipelineRunTaskLister
}

// NewPipelineStatusHandler creates a read-only pipeline status handler.
func NewPipelineStatusHandler(repo PipelineRunTaskLister) *PipelineStatusHandler {
	return &PipelineStatusHandler{repo: repo}
}

type pipelineStageStatus struct {
	Stage     string `json:"stage"`
	Status    string `json:"status"`
	LastError string `json:"lastError,omitempty"`
}

// Get handles GET /internal/orchestrator/pipeline-status.
func (h *PipelineStatusHandler) Get(c *gin.Context) {
	runID := strings.TrimSpace(c.Query("runId"))
	fileMD5 := strings.TrimSpace(c.Query("fileMd5"))
	if !pipelineRunIDPattern.MatchString(runID) || !pipelineFileMD5Pattern.MatchString(fileMD5) {
		c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "message": "invalid runId or fileMd5", "data": nil})
		return
	}
	tasks, err := h.repo.ListByRunAndFile(runID, fileMD5)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": http.StatusInternalServerError, "message": "failed to read pipeline status", "data": nil})
		return
	}

	byStage := make(map[string][]model.PipelineTask, len(pipelineStages))
	for _, task := range tasks {
		byStage[task.Stage] = append(byStage[task.Stage], task)
	}
	complete := true
	stages := make([]pipelineStageStatus, 0, len(pipelineStages))
	for _, stage := range pipelineStages {
		status := aggregatePipelineStage(stage, byStage[stage])
		if status.Status != model.PipelineStatusSuccess {
			complete = false
		}
		stages = append(stages, status)
	}
	c.JSON(http.StatusOK, gin.H{"runId": runID, "fileMd5": fileMD5, "complete": complete, "stages": stages})
}

func aggregatePipelineStage(stage string, tasks []model.PipelineTask) pipelineStageStatus {
	if len(tasks) == 0 {
		return pipelineStageStatus{Stage: stage, Status: "MISSING"}
	}
	allSuccess := true
	for _, task := range tasks {
		if task.Status == model.PipelineStatusFailed {
			return pipelineStageStatus{Stage: stage, Status: model.PipelineStatusFailed, LastError: "pipeline stage failed"}
		}
		if task.Status != model.PipelineStatusSuccess {
			allSuccess = false
		}
	}
	if allSuccess {
		return pipelineStageStatus{Stage: stage, Status: model.PipelineStatusSuccess}
	}
	return pipelineStageStatus{Stage: stage, Status: model.PipelineStatusProcessing}
}
