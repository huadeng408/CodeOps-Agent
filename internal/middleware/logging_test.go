package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"code-agent/pkg/log"

	"github.com/gin-gonic/gin"
)

func TestRequestLoggerDoesNotPersistKnowledgeSearchBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	readLog, writeLog, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = writeLog
	log.Init("info", "json", "")
	type captureResult struct {
		body []byte
		err  error
	}
	captured := make(chan captureResult, 1)
	go func() {
		body, readErr := io.ReadAll(readLog)
		captured <- captureResult{body: body, err: readErr}
	}()
	t.Cleanup(func() {
		os.Stdout = originalStdout
		log.Init("info", "json", "")
		_ = writeLog.Close()
		_ = readLog.Close()
	})

	router := gin.New()
	router.Use(RequestLogger())
	requestBody := `{"user":{"id":7},"query":"private retrieval query","topK":5}`
	responseBody := "private retrieved document body"
	router.POST("/internal/orchestrator/knowledge-search", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatalf("handler failed to read request body: %v", err)
		}
		if string(body) != requestBody {
			t.Fatalf("handler received %q, want %q", body, requestBody)
		}
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"textContent": responseBody,
			},
		})
	})

	request := httptest.NewRequest(http.MethodPost, "/internal/orchestrator/knowledge-search", bytes.NewBufferString(requestBody))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), request)
	log.Sync()
	if err := writeLog.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = originalStdout
	result := <-captured
	if result.err != nil {
		t.Fatal(result.err)
	}
	logged := result.body
	log.Init("info", "json", "")
	text := string(logged)
	for _, content := range []string{
		"private retrieval query",
		responseBody,
		"requestBody",
		"responseBody",
	} {
		if strings.Contains(text, content) {
			t.Fatalf("request log contains body content %q: %s", content, text)
		}
	}
	for _, diagnostic := range []string{
		`"statusCode":200`,
		`"path":"/internal/orchestrator/knowledge-search"`,
		`"requestBytes":`,
		`"responseBytes":`,
	} {
		if !strings.Contains(text, diagnostic) {
			t.Fatalf("request log is missing diagnostic %q: %s", diagnostic, text)
		}
	}
}
