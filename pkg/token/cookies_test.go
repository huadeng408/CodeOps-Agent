package token

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetAuthCookiesUsesHttpOnlySameSiteCredentials(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://localhost/login", nil)

	SetAuthCookies(recorder, request, "access-value", "refresh-value", 60, 120)

	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookie count = %d, want 2", len(cookies))
	}
	assertAuthCookie(t, cookies[0], AccessTokenCookieName, "access-value", 60, false)
	assertAuthCookie(t, cookies[1], RefreshTokenCookieName, "refresh-value", 120, false)
}

func TestSetAuthCookiesUsesSecureForTLS(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "https://example.test/login", nil)
	request.TLS = &tls.ConnectionState{}

	SetAuthCookies(recorder, request, "access-value", "refresh-value", 60, 120)

	for _, cookie := range recorder.Result().Cookies() {
		if !cookie.Secure {
			t.Fatalf("cookie %q Secure = false for TLS request", cookie.Name)
		}
	}
}

func TestClearAuthCookiesExpiresBothCredentials(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://localhost/logout", nil)

	ClearAuthCookies(recorder, request)

	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookie count = %d, want 2", len(cookies))
	}
	for _, cookie := range cookies {
		if cookie.Value != "" || cookie.MaxAge >= 0 {
			t.Fatalf("cookie %q was not cleared: value=%q maxAge=%d", cookie.Name, cookie.Value, cookie.MaxAge)
		}
		if !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode {
			t.Fatalf("cleared cookie %q lost security attributes: %#v", cookie.Name, cookie)
		}
	}
}

func assertAuthCookie(t *testing.T, cookie *http.Cookie, name, value string, maxAge int, secure bool) {
	t.Helper()
	if cookie.Name != name || cookie.Value != value {
		t.Fatalf("cookie = %q/%q, want %q/%q", cookie.Name, cookie.Value, name, value)
	}
	if cookie.Path != "/" || cookie.MaxAge != maxAge || !cookie.HttpOnly || cookie.Secure != secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie %q attributes = %#v", cookie.Name, cookie)
	}
}
