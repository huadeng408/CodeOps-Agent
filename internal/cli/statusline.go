package cli

import (
	"fmt"
	"strings"

	"code-agent/internal/metrics"
)

type StatusLine struct{ CostUnknown bool }

func NewStatusLine() *StatusLine {
	return &StatusLine{}
}

func (s *StatusLine) Format(snapshot metrics.SessionMetrics) string {
	return s.FormatWidth(snapshot, "status", 80)
}

func (s *StatusLine) FormatWidth(snapshot metrics.SessionMetrics, mode string, width int) string {
	cost := fmt.Sprintf("cost $%.4f", snapshot.TotalCost)
	if s.CostUnknown {
		cost = "cost unknown"
	}
	cachePart := ""
	if snapshot.TotalCachedTokens > 0 {
		ratio := 0.0
		if snapshot.TotalTokensIn > 0 {
			ratio = float64(snapshot.TotalCachedTokens) / float64(snapshot.TotalTokensIn)
		}
		cachePart = fmt.Sprintf("  cache %d (%.0f%%)", snapshot.TotalCachedTokens, ratio*100)
	}
	fields := []string{
		strings.TrimSpace(mode),
		fmt.Sprintf("errors %d", snapshot.Errors),
		fmt.Sprintf("tokens %d in / %d out%s", snapshot.TotalTokensIn, snapshot.TotalTokensOut, cachePart),
		cost,
		fmt.Sprintf("tools %d", snapshot.ToolCalls),
		fmt.Sprintf("turns %d", snapshot.Turns),
		"/help",
	}
	for len(fields) > 2 && cellWidth(strings.Join(fields, " | ")) > width {
		fields = fields[:len(fields)-1]
	}
	return strings.Join(fields, " | ")
}
