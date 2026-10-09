package middleware_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"code-agent/internal/middleware"

	"github.com/gin-gonic/gin"
)

func TestPanicRecoveryDoesNotLogCredentials(t *testing.T) {
	var output bytes.Buffer
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &output
	defer func() { gin.DefaultErrorWriter = previous }()
	router := gin.New()
	router.Use(middleware.RedactedRecovery())
	router.GET("/", func(*gin.Context) { panic("private-panic-marker") })
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Cookie", "access_token=private-cookie-marker")
	request.Header.Set("X-Api-Key", "private-header-marker")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("panic recovery status = %d", response.Code)
	}
	for _, marker := range []string{"private-cookie-marker", "private-header-marker", "private-panic-marker"} {
		if bytes.Contains(output.Bytes(), []byte(marker)) {
			t.Fatal("panic recovery leaked credential-bearing request or error data")
		}
	}
}
