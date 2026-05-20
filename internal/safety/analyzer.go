package safety

import (
	"strings"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type Analysis struct {
	Allowed bool
	Level   RiskLevel
	Reason  string
}

type Analyzer struct{}

func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

func (a *Analyzer) AnalyzeCommand(command string) Analysis {
	lower := strings.ToLower(strings.TrimSpace(command))
	switch {
	case lower == "":
		return Analysis{Allowed: false, Level: RiskLow, Reason: "empty command"}
	case strings.Contains(lower, "rm -rf /"):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "destructive delete blocked"}
	case strings.Contains(lower, "format c:"):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "destructive disk format blocked"}
	case strings.Contains(lower, "curl ") && strings.Contains(lower, "| sh"):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "piped shell install blocked"}
	case strings.HasPrefix(lower, "git push --force"):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "force push blocked"}
	case strings.HasPrefix(lower, "git "):
		return a.AnalyzeGit(strings.Fields(lower)[1:])
	default:
		return Analysis{Allowed: true, Level: RiskLow, Reason: "no obvious risk detected"}
	}
}

func (a *Analyzer) AnalyzeGit(args []string) Analysis {
	if len(args) == 0 {
		return Analysis{Allowed: true, Level: RiskLow, Reason: "git status-like command"}
	}
	if IsSafeGitSubcommand(args[0]) {
		return Analysis{Allowed: true, Level: RiskLow, Reason: "safe git subcommand"}
	}
	return Analysis{Allowed: false, Level: RiskMedium, Reason: "unsupported git subcommand"}
}
