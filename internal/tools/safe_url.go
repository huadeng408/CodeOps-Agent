package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxRedirects caps how many HTTP redirects WebFetch/WebSearch will follow.
const maxRedirects = 3

// safeHTTPTimeout is the per-request timeout for safe outbound HTTP.
const safeHTTPTimeout = 15 * time.Second

// blockedHostError is returned when a URL resolves to a non-public address.
type blockedHostError struct{ host, reason string }

func (e *blockedHostError) Error() string {
	return fmt.Sprintf("refused: host %s is %s — internal addresses are blocked", e.host, e.reason)
}

// validateURL checks that rawURL uses an http(s) scheme and a non-empty host.
// It does NOT check the resolved IP (use newSafeClient for that — DNS rebinding
// means the IP can change between validation and dial).
func validateURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("scheme %q not allowed (only http/https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("url host is empty")
	}
	return parsed, nil
}

// isBlockedAddr reports whether addr is a non-public IP that must not be fetched
// (loopback, private, link-local, unicast, multicast, unspecified). Returns the
// human-readable reason when blocked.
func isBlockedAddr(ip net.IP) (bool, string) {
	switch {
	case ip == nil:
		return true, "unresolved"
	case ip.IsUnspecified():
		return true, "unspecified (0.0.0.0/::)"
	case ip.IsLoopback():
		return true, "loopback"
	case ip.IsPrivate():
		return true, "private (RFC1918)"
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return true, "link-local (169.254/fe80)"
	case ip.IsMulticast():
		return true, "multicast"
	}
	// AWS / cloud metadata endpoints are a private-address SSRF target. The
	// classic 169.254.169.254 is link-local (already blocked above); IPv6
	// metadata fd00:ec2::254 falls in IsPrivate. No extra rule needed.
	return false, ""
}

// newSafeClient returns an *http.Client whose transport resolves the target
// host and rejects any non-public address at DIAL time (defeating DNS rebinding),
// and whose CheckRedirect re-validates every redirect target and caps the hop
// count. This is the single choke point for WebFetch/WebSearch SSRF defense.
//
// allowPrivate lifts the private/loopback block — intended only for tests and
// trusted local providers; production callers MUST pass false.
func newSafeClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			// Resolve here so we inspect every A/AAAA record, not just the first.
			ips, lookupErr := net.DefaultResolver.LookupIPAddr(ctx, host)
			if lookupErr != nil {
				return nil, lookupErr
			}
			if !allowPrivate {
				for _, iprec := range ips {
					if blocked, reason := isBlockedAddr(iprec.IP); blocked {
						return nil, &blockedHostError{host: host, reason: reason}
					}
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout: safeHTTPTimeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			// Re-validate scheme/host of each redirect target.
			if _, err := validateURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}
