package token

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	AccessTokenCookieName  = "codeagent_access_token"
	RefreshTokenCookieName = "codeagent_refresh_token"
)

var ErrMissingAccessToken = errors.New("access token is required")

// AccessTokenFromRequest accepts an Authorization header or the HttpOnly
// access cookie. Query-string tokens are intentionally unsupported because
// URLs are routinely logged by browsers, proxies, and observability systems.
func AccessTokenFromRequest(r *http.Request) (string, error) {
	if r == nil {
		return "", ErrMissingAccessToken
	}
	if raw := strings.TrimSpace(r.Header.Get("Authorization")); raw != "" {
		parts := strings.Fields(raw)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			return "", errors.New("invalid authorization header")
		}
		return strings.TrimSpace(parts[1]), nil
	}
	cookie, err := r.Cookie(AccessTokenCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", ErrMissingAccessToken
	}
	return strings.TrimSpace(cookie.Value), nil
}

// RefreshTokenFromRequest reads a body-independent refresh credential. The
// legacy X-Refresh-Token header remains accepted for non-browser clients;
// browser clients use the HttpOnly cookie.
func RefreshTokenFromRequest(r *http.Request) (string, error) {
	if r == nil {
		return "", ErrMissingAccessToken
	}
	if raw := strings.TrimSpace(r.Header.Get("X-Refresh-Token")); raw != "" {
		return raw, nil
	}
	cookie, err := r.Cookie(RefreshTokenCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", ErrMissingAccessToken
	}
	return strings.TrimSpace(cookie.Value), nil
}

// SetAuthCookies writes short-lived, SameSite, HttpOnly credentials. Secure is
// enabled for TLS requests and can be forced behind a TLS-terminating proxy
// with CODE_AGENT_COOKIE_SECURE=true.
func SetAuthCookies(w http.ResponseWriter, r *http.Request, access, refresh string, accessMaxAge, refreshMaxAge int) {
	secure := cookieSecure(r)
	setCookie(w, &http.Cookie{
		Name: AccessTokenCookieName, Value: access, Path: "/", MaxAge: accessMaxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	setCookie(w, &http.Cookie{
		Name: RefreshTokenCookieName, Value: refresh, Path: "/", MaxAge: refreshMaxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func ClearAuthCookies(w http.ResponseWriter, r *http.Request) {
	secure := cookieSecure(r)
	for _, name := range []string{AccessTokenCookieName, RefreshTokenCookieName} {
		setCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		})
	}
}

func cookieSecure(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CODE_AGENT_COOKIE_SECURE")), "true") {
		return true
	}
	return r != nil && r.TLS != nil
}

func setCookie(w http.ResponseWriter, cookie *http.Cookie) {
	if w != nil && cookie != nil {
		http.SetCookie(w, cookie)
	}
}
