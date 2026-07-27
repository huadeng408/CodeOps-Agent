package cli

import (
	"fmt"

	"code-agent/internal/metrics"
)

type StatusLine struct{}

func NewStatusLine() *StatusLine {
	return &StatusLine{}
}

func (s *StatusLine) Format(snapshot metrics.SessionMetrics) string {
	cachePart := ""
	if snapshot.TotalCachedTokens > 0 {
		ratio := 0.0
		if snapshot.TotalTokensIn > 0 {
			ratio = float64(snapshot.TotalCachedTokens) / float64(snapshot.TotalTokensIn)
		}
		cachePart = fmt.Sprintf("  cache %d (%.0f%%)", snapshot.TotalCachedTokens, ratio*100)
	}
	return fmt.Sprintf("status  tokens %d in / %d out%s  cost $%.4f  tools %d  turns %d  errors %d",
		snapshot.TotalTokensIn,
		snapshot.TotalTokensOut,
		cachePart,
		snapshot.TotalCost,
		snapshot.ToolCalls,
		snapshot.Turns,
		snapshot.Errors,
	)
}
