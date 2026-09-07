package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/service"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
)

type authCookieService struct {
	service.UserService
	loginAccess        string
	loginRefresh       string
	refreshAccess      string
	refreshRefresh     string
	refreshInput       string
	logoutAccessInput  string
	logoutRefreshInput string
}

func (s *authCookieService) Login(string, string) (string, string, error) {
	return s.loginAccess, s.loginRefresh, nil
}

func (s *authCookieService) RefreshToken(value string) (string, string, error) {
	s.refreshInput = value
	return s.refreshAccess, s.refreshRefresh, nil
}

func (s *authCookieService) Logout(access, refresh string) error {
	s.logoutAccessInput = access
	s.logoutRefreshInput = refresh
	return nil
}

func TestLoginWritesBrowserAuthCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &authCookieService{loginAccess: "new-access", loginRefresh: "new-refresh"}
	router := gin.New()
	router.POST("/login", NewUserHandler(stub).Login)

	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("{\"email\":\"browser@example.test\",\"password\":\"secret\"}"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	assertHandlerAuthCookies(t, response.Result().Cookies(), "new-access", "new-refresh", false)
}

func TestRefreshReadsCookieAndRotatesBothCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &authCookieService{refreshAccess: "rotated-access", refreshRefresh: "rotated-refresh"}
	router := gin.New()
	router.POST("/refresh", NewAuthHandler(stub).RefreshToken)

	request := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	request.AddCookie(&http.Cookie{Name: token.RefreshTokenCookieName, Value: "old-refresh"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if stub.refreshInput != "old-refresh" {
		t.Fatalf("refresh input = %q, want cookie credential", stub.refreshInput)
	}
	assertHandlerAuthCookies(t, response.Result().Cookies(), "rotated-access", "rotated-refresh", false)
}

func TestLogoutUsesCookiesAndClearsBothCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &authCookieService{}
	router := gin.New()
	router.POST("/logout", func(c *gin.Context) {
		c.Set("user", &model.User{Username: "browser-user"})
		NewUserHandler(stub).Logout(c)
	})

	request := httptest.NewRequest(http.MethodPost, "/logout", nil)
	request.AddCookie(&http.Cookie{Name: token.AccessTokenCookieName, Value: "access-to-revoke"})
	request.AddCookie(&http.Cookie{Name: token.RefreshTokenCookieName, Value: "refresh-to-revoke"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if stub.logoutAccessInput != "access-to-revoke" || stub.logoutRefreshInput != "refresh-to-revoke" {
		t.Fatalf("logout inputs = %q/%q", stub.logoutAccessInput, stub.logoutRefreshInput)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cleared cookie count = %d, want 2", len(cookies))
	}
	for _, cookie := range cookies {
		if cookie.Value != "" || cookie.MaxAge >= 0 || !cookie.HttpOnly {
			t.Fatalf("cookie %q was not securely cleared: %#v", cookie.Name, cookie)
		}
	}
}

func assertHandlerAuthCookies(t *testing.T, cookies []*http.Cookie, access, refresh string, secure bool) {
	t.Helper()
	if len(cookies) != 2 {
		t.Fatalf("cookie count = %d, want 2", len(cookies))
	}
	want := map[string]string{
		token.AccessTokenCookieName:  access,
		token.RefreshTokenCookieName: refresh,
	}
	for _, cookie := range cookies {
		if cookie.Value != want[cookie.Name] || !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || cookie.Secure != secure {
			t.Fatalf("cookie %q attributes/value = %#v", cookie.Name, cookie)
		}
	}
}
