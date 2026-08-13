package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"code-agent/internal/model"

	"github.com/gin-gonic/gin"
)

type recordingPipelineRunLister struct {
	tasks []model.PipelineTask
	err   error
	runID string
	md5   string
}

func (r *recordingPipelineRunLister) ListByRunAndFile(runID, fileMD5 string) ([]model.PipelineTask, error) {
	r.runID, r.md5 = runID, fileMD5
	return r.tasks, r.err
}

func TestPipelineStatusReportsOnlyCurrentRunAndFailsClosedOnMissingStage(t *testing.T) {
	repo := &recordingPipelineRunLister{tasks: []model.PipelineTask{
		{RunID: "run-1", FileMD5: "0123456789abcdef0123456789abcdef", Stage: "parse", Status: model.PipelineStatusSuccess},
		{RunID: "run-1", FileMD5: "0123456789abcdef0123456789abcdef", Stage: "chunk", Status: model.PipelineStatusSuccess},
		{RunID: "run-1", FileMD5: "0123456789abcdef0123456789abcdef", Stage: "embed", Status: model.PipelineStatusSuccess},
	}}
	recorder := servePipelineStatus(t, repo, "run-1", "0123456789abcdef0123456789abcdef")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Complete bool `json:"complete"`
		Stages   []struct {
			Stage  string `json:"stage"`
			Status string `json:"status"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Complete {
		t.Fatal("missing index stage must not be reported complete")
	}
	if len(body.Stages) != 4 || body.Stages[3].Stage != "index" || body.Stages[3].Status != "MISSING" {
		t.Fatalf("stages=%+v, want ordered parse/chunk/embed/index with missing index", body.Stages)
	}
	if repo.runID != "run-1" || repo.md5 != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("repo query = %q/%q", repo.runID, repo.md5)
	}
}

func TestPipelineStatusReportsFailedStageWithoutLeakingStoredError(t *testing.T) {
	repo := &recordingPipelineRunLister{tasks: []model.PipelineTask{
		{Stage: "parse", Status: model.PipelineStatusFailed, LastError: "Authorization: Bearer secret-token mysql://root:password@host"},
	}}
	recorder := servePipelineStatus(t, repo, "run-2", "abcdef0123456789abcdef0123456789")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{"secret-token", "password", "Bearer", "mysql://"} {
		if containsCaseInsensitive(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
	if !containsCaseInsensitive(body, "pipeline stage failed") {
		t.Fatalf("response lacks sanitized failure summary: %s", body)
	}
}

func TestPipelineStatusRejectsMalformedScopeWithoutRepositoryCall(t *testing.T) {
	for _, tc := range []struct{ runID, md5 string }{
		{"", "0123456789abcdef0123456789abcdef"},
		{"run with spaces", "0123456789abcdef0123456789abcdef"},
		{"run-1", "not-an-md5"},
	} {
		repo := &recordingPipelineRunLister{}
		recorder := servePipelineStatus(t, repo, tc.runID, tc.md5)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("scope %q/%q status=%d body=%s", tc.runID, tc.md5, recorder.Code, recorder.Body.String())
		}
		if repo.runID != "" || repo.md5 != "" {
			t.Fatal("malformed scope reached repository")
		}
	}
}

func TestPipelineStatusSanitizesRepositoryFailure(t *testing.T) {
	repo := &recordingPipelineRunLister{err: errors.New("mysql password=hunter2")}
	recorder := servePipelineStatus(t, repo, "run-3", "fedcba9876543210fedcba9876543210")
	if recorder.Code != http.StatusInternalServerError || containsCaseInsensitive(recorder.Body.String(), "hunter2") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func servePipelineStatus(t *testing.T, repo PipelineRunTaskLister, runID, fileMD5 string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/internal/orchestrator/pipeline-status", NewPipelineStatusHandler(repo).Get)
	query := url.Values{"runId": []string{runID}, "fileMd5": []string{fileMD5}}
	request := httptest.NewRequest(http.MethodGet, "/internal/orchestrator/pipeline-status?"+query.Encode(), nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func containsCaseInsensitive(value, substring string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(substring))
}
