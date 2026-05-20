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
	return fmt.Sprintf("Tokens: %d in / %d out | Cost: $%.4f | Tools: %d | Turns: %d | Errors: %d",
		snapshot.TotalTokensIn,
		snapshot.TotalTokensOut,
		snapshot.TotalCost,
		snapshot.ToolCalls,
		snapshot.Turns,
		snapshot.Errors,
	)
}
