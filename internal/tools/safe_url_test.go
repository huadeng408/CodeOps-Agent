package tools

import (
	"net"
	"testing"
)

func TestIsBlockedAddr(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},          // loopback v4
		{"::1", true},                // loopback v6
		{"10.0.0.1", true},           // private 10/8
		{"172.16.0.1", true},         // private 172.16/12
		{"192.168.1.1", true},        // private 192.168/16
		{"169.254.169.254", true},    // link-local AWS metadata
		{"fe80::1", true},            // link-local v6
		{"0.0.0.0", true},            // unspecified
		{"224.0.0.1", true},          // multicast
		{"8.8.8.8", false},           // public
		{"1.1.1.1", false},           // public
		{"2606:4700:4700::1111", false}, // public v6
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("parse %s: invalid test IP", c.ip)
		}
		got, reason := isBlockedAddr(ip)
		if got != c.blocked {
			t.Errorf("isBlockedAddr(%s) = %v (%s), want %v", c.ip, got, reason, c.blocked)
		}
	}
}

func TestValidateURL(t *testing.T) {
	bad := []string{
		"file:///etc/passwd",
		"gopher://x",
		"ftp://example.com",
		"http://",
		"javascript:alert(1)",
		"",
	}
	for _, u := range bad {
		if _, err := validateURL(u); err == nil {
			t.Errorf("validateURL(%q) unexpectedly succeeded", u)
		}
	}
	good := []string{
		"http://example.com",
		"https://example.com/path?q=1",
		"HTTPS://UPPER.example.com",
	}
	for _, u := range good {
		if _, err := validateURL(u); err != nil {
			t.Errorf("validateURL(%q) failed: %v", u, err)
		}
	}
}

// TestSafeClientBlocksLoopback confirms the dialer rejects a loopback target,
// proving SSRF defense works end-to-end (not just at the IP classifier).
func TestSafeClientBlocksLoopback(t *testing.T) {
	client := newSafeClient(false) // production default: block private/loopback
	// Spin up a listener we will NOT actually serve — the dial must be
	// refused before any HTTP exchange because 127.0.0.1 is loopback.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on loopback for test: %v", err)
	}
	defer ln.Close()
	// Build a URL pointing at the loopback listener.
	resp, err := client.Get("http://" + ln.Addr().String() + "/")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("safe client fetched loopback address %s — SSRF not blocked", ln.Addr())
	}
}
