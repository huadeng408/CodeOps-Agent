package cli

import (
	"strings"
	"testing"

	"code-agent/internal/metrics"
)

func TestStatusLineFormatWidthPrioritizesErrorsAndMode(t *testing.T) {
	snapshot := metrics.SessionMetrics{TotalTokensIn: 1200, TotalTokensOut: 300, TotalCost: 0.125, ToolCalls: 8, Turns: 4, Errors: 2}
	wide := NewStatusLine().FormatWidth(snapshot, "chat", 120)
	for _, want := range []string{"chat", "errors 2", "tokens 1200 in / 300 out", "cost $0.1250", "tools 8", "turns 4", "/help"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide status missing %q: %q", want, wide)
		}
	}
	narrow := NewStatusLine().FormatWidth(snapshot, "chat", 38)
	if !strings.Contains(narrow, "chat") || !strings.Contains(narrow, "errors 2") || strings.Contains(narrow, "/help") || strings.Contains(narrow, "tools 8") {
		t.Fatalf("narrow priority is wrong: %q", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if cellWidth(line) > 38 {
			t.Fatalf("status line width %d > 38: %q", cellWidth(line), line)
		}
	}
}

func TestStatusLineFormatWidthHasReadableMinimum(t *testing.T) {
	got := NewStatusLine().FormatWidth(metrics.SessionMetrics{}, "plan", 30)
	if got != "plan | errors 0" {
		t.Fatalf("minimal status = %q", got)
	}
}

func TestStatusLineDoesNotReportUnknownPriceAsZero(t *testing.T) {
	line := NewStatusLine()
	line.CostUnknown = true
	got := line.FormatWidth(metrics.SessionMetrics{TotalTokensIn: 3}, "status", 120)
	if !strings.Contains(got, "cost unknown") || strings.Contains(got, "$0") {
		t.Fatalf("unknown price was rendered as an amount: %s", got)
	}
}
