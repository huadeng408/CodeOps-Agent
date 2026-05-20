package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

func executeWebFetch(ctx context.Context, _ string, args map[string]any) (ToolResult, error) {
	url, ok := stringArg(args, "url")
	if !ok || url == "" {
		return ToolResult{Name: "WebFetch", Error: "url is required"}, fmt.Errorf("url is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ToolResult{Name: "WebFetch", Error: err.Error()}, err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ToolResult{Name: "WebFetch", Error: err.Error()}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ToolResult{Name: "WebFetch", Error: err.Error()}, err
	}

	output, truncated := normalizeOutput(string(body), 50_000)
	return ToolResult{
		Name:      "WebFetch",
		Output:    fmt.Sprintf("status=%d\n%s", resp.StatusCode, output),
		Truncated: truncated,
	}, nil
}
