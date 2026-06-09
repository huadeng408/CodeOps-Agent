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
	return fmt.Sprintf("status  tokens %d in / %d out  cost $%.4f  tools %d  turns %d  errors %d",
		snapshot.TotalTokensIn,
		snapshot.TotalTokensOut,
		snapshot.TotalCost,
		snapshot.ToolCalls,
		snapshot.Turns,
		snapshot.Errors,
	)
}
