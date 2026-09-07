package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/service"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
)

type authTestUserService struct {
	service.UserService
	user *model.User
}

func (s authTestUserService) IsTokenBlacklisted(string) (bool, error) { return false, nil }
func (s authTestUserService) GetProfile(string) (*model.User, error)  { return s.user, nil }

func TestAuthMiddlewareAcceptsHttpOnlyAccessCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := token.NewJWTManager("test-secret", 1, 1)
	access, err := manager.GenerateToken(7, "cookie-user", "USER")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(AuthMiddleware(manager, authTestUserService{user: &model.User{ID: 7, Username: "cookie-user"}}))
	router.GET("/protected", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(&http.Cookie{Name: accessTokenCookieName, Value: access})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("cookie auth status = %d, want %d; body=%s", response.Code, http.StatusNoContent, response.Body.String())
	}
}

func TestAuthMiddlewareRejectsQueryToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := token.NewJWTManager("test-secret", 1, 1)
	access, err := manager.GenerateToken(7, "query-user", "USER")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(AuthMiddleware(manager, authTestUserService{user: &model.User{ID: 7, Username: "query-user"}}))
	router.GET("/protected", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/protected?token="+access, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("query token status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
