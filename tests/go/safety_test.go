package codeagent_test

import (
	"testing"

	"code-agent/internal/safety"
)

func TestAnalyzerBlocksDangerousCommands(t *testing.T) {
	analyzer := safety.NewAnalyzer()

	if got := analyzer.AnalyzeCommand("rm -rf /"); got.Allowed {
		t.Fatalf("rm -rf / should be blocked: %+v", got)
	}
	if got := analyzer.AnalyzeCommand("git status"); !got.Allowed {
		t.Fatalf("git status should be allowed: %+v", got)
	}
}
