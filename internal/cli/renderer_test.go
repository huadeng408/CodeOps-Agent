package cli

import (
	"bytes"
	"strings"
	"testing"

	"code-agent/internal/metrics"
	"code-agent/internal/orchestrator"
)

func TestStreamRendererFormatsBlockAsPanel(t *testing.T) {
	out := &bytes.Buffer{}
	renderer := NewStreamRenderer(out)

	renderer.PrintBlock("config", []string{"model: gpt-4o", "context window: 256000"})

	rendered := out.String()
	if !strings.Contains(rendered, "╭─ config") ||
		!strings.Contains(rendered, "│ model: gpt-4o") ||
		!strings.Contains(rendered, "╰") {
		t.Fatalf("block was not rendered as a panel: %q", rendered)
	}
}

func TestStreamRendererFormatsAssistantMessage(t *testing.T) {
	out := &bytes.Buffer{}
	renderer := NewStreamRenderer(out)

	renderer.PrintAssistant("first line\nsecond line")

	rendered := out.String()
	if !strings.Contains(rendered, "assistant") ||
		!strings.Contains(rendered, "first line") ||
		!strings.Contains(rendered, "second line") {
		t.Fatalf("assistant message was not rendered: %q", rendered)
	}
}

func TestStatusLineUsesCompactConversationStyle(t *testing.T) {
	status := NewStatusLine().Format(metrics.SessionMetrics{
		TotalTokensIn:  100,
		TotalTokensOut: 50,
		TotalCost:      0.00075,
		ToolCalls:      2,
		Turns:          3,
		Errors:         1,
	})

	if status != "status  tokens 100 in / 50 out  cost $0.0008  tools 2  turns 3  errors 1" {
		t.Fatalf("unexpected status line: %q", status)
	}
}

func TestFormatToolProgress(t *testing.T) {
	line := formatToolProgress(&orchestrator.ToolProgress{
		ToolName:  "Read",
		Phase:     "finish",
		Index:     2,
		Total:     3,
		ExitCode:  1,
		Error:     strings.Repeat("x", 160),
		Truncated: true,
	})

	if !strings.Contains(line, "tool 2/3 Read finished exit=1") {
		t.Fatalf("missing progress summary: %q", line)
	}
	if !strings.Contains(line, "truncated=true") || len(line) > 180 {
		t.Fatalf("unexpected progress details: %q", line)
	}
}
