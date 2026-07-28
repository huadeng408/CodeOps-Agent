package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (e *Executor) executeWebSearch(ctx context.Context, args map[string]any) (ToolResult, error) {
	query, ok := stringArg(args, "query", "q")
	if !ok || strings.TrimSpace(query) == "" {
		return ToolResult{Name: "WebSearch", Error: "query is required"}, fmt.Errorf("query is required")
	}

	endpoint, _ := stringArg(args, "endpoint", "url")
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "https://api.duckduckgo.com/"
	}
	// SSRF defense: validate scheme/host before dial (safe client also re-checks IP).
	if _, err := validateURL(endpoint); err != nil {
		return ToolResult{Name: "WebSearch", Error: err.Error(), ExitCode: 1}, err
	}
	requestURL, err := webSearchURL(endpoint, query)
	if err != nil {
		return ToolResult{Name: "WebSearch", Error: err.Error(), ExitCode: 1}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return ToolResult{Name: "WebSearch", Error: err.Error(), ExitCode: 1}, err
	}
	req.Header.Set("User-Agent", "code-agent/0.1")

	resp, err := newSafeClient(e.httpAllowPrivate).Do(req)
	if err != nil {
		return ToolResult{Name: "WebSearch", Error: err.Error(), ExitCode: 1}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("search request failed with status %d", resp.StatusCode)
		return ToolResult{Name: "WebSearch", Error: err.Error(), ExitCode: 1}, err
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ToolResult{Name: "WebSearch", Error: err.Error(), ExitCode: 1}, err
	}
	output := formatDuckDuckGoResults(payload)
	if strings.TrimSpace(output) == "" {
		output = "no results"
	}
	output, truncated := e.TruncateOutput(output)
	return ToolResult{Name: "WebSearch", Output: output, Truncated: truncated}, nil
}

func webSearchURL(endpoint, query string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	if values.Get("q") == "" {
		values.Set("q", query)
	}
	if values.Get("format") == "" {
		values.Set("format", "json")
	}
	if values.Get("no_html") == "" {
		values.Set("no_html", "1")
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func formatDuckDuckGoResults(payload map[string]any) string {
	lines := []string{}
	if heading := stringFromPayload(payload, "Heading"); heading != "" {
		lines = append(lines, "# "+heading)
	}
	if abstract := stringFromPayload(payload, "AbstractText"); abstract != "" {
		lines = append(lines, abstract)
	}
	if abstractURL := stringFromPayload(payload, "AbstractURL"); abstractURL != "" {
		lines = append(lines, abstractURL)
	}

	if related, ok := payload["RelatedTopics"].([]any); ok {
		for _, item := range flattenRelatedTopics(related) {
			text := strings.TrimSpace(stringFromPayload(item, "Text"))
			firstURL := strings.TrimSpace(stringFromPayload(item, "FirstURL"))
			if text == "" && firstURL == "" {
				continue
			}
			line := "- " + text
			if firstURL != "" {
				line += " — " + firstURL
			}
			lines = append(lines, line)
			if len(lines) >= 12 {
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func flattenRelatedTopics(items []any) []map[string]any {
	out := []map[string]any{}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if nested, ok := item["Topics"].([]any); ok {
			out = append(out, flattenRelatedTopics(nested)...)
			continue
		}
		out = append(out, item)
	}
	return out
}

func stringFromPayload(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
