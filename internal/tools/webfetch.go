package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

func (e *Executor) executeWebFetch(ctx context.Context, args map[string]any) (ToolResult, error) {
	urlStr, ok := stringArg(args, "url")
	if !ok || urlStr == "" {
		return ToolResult{Name: "WebFetch", Error: "url is required"}, fmt.Errorf("url is required")
	}
	// SSRF defense: reject non-http(s) schemes; the safe client re-checks the
	// resolved IP at dial time to defeat DNS rebinding.
	parsed, err := validateURL(urlStr)
	if err != nil {
		return ToolResult{Name: "WebFetch", Error: err.Error()}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return ToolResult{Name: "WebFetch", Error: err.Error()}, err
	}

	resp, err := newSafeClient(e.httpAllowPrivate).Do(req)
	if err != nil {
		return ToolResult{Name: "WebFetch", Error: err.Error()}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ToolResult{Name: "WebFetch", Error: err.Error()}, err
	}

	return ToolResult{
		Name:   "WebFetch",
		Output: fmt.Sprintf("status=%d\n%s", resp.StatusCode, string(body)),
	}, nil
}
