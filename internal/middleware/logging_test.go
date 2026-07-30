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

func TestRedactJSONLogBodyRecursively(t *testing.T) {
	body := []byte(`{
		"username":"alice",
		"password":"request-password-secret",
		"nested":{"accessToken":"access-token-secret","safe":"visible"},
		"items":[{"refresh_token":"refresh-token-secret"},{"X-Internal-Token":"internal-token-secret"}],
		"Authorization":"Bearer authorization-secret"
	}`)

	redacted := redactJSONLogBody(body)
	for _, secret := range []string{
		"request-password-secret",
		"access-token-secret",
		"refresh-token-secret",
		"internal-token-secret",
		"authorization-secret",
	} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted JSON contains sensitive value %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, `"safe":"visible"`) || !strings.Contains(redacted, `"password":"[REDACTED]"`) {
		t.Fatalf("redacted JSON lost safe data or field names: %s", redacted)
	}
}

func TestRedactJSONLogBodyPreservesNonJSON(t *testing.T) {
	const body = "plain request body with existing formatting\n"
	if got := redactJSONLogBody([]byte(body)); got != body {
		t.Fatalf("non-JSON body changed: got %q want %q", got, body)
	}
}

func TestRequestLoggerDoesNotPersistSensitiveJSONOrHeaders(t *testing.T) {
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
	router.POST("/login", func(c *gin.Context) {
		_, _ = io.Copy(io.Discard, c.Request.Body)
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"accessToken":  "response-access-token-secret",
				"refreshToken": "response-refresh-token-secret",
			},
		})
	})

	request := httptest.NewRequest(http.MethodPost, "/login", bytes.NewBufferString(`{"username":"alice","password":"request-password-secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer header-authorization-secret")
	request.Header.Set("X-Internal-Token", "header-internal-token-secret")
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
	for _, secret := range []string{
		"request-password-secret",
		"response-access-token-secret",
		"response-refresh-token-secret",
		"header-authorization-secret",
		"header-internal-token-secret",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("request log contains sensitive value %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("request log does not contain redaction marker: %s", text)
	}
}
