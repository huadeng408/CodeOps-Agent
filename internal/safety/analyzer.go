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
	trimmed := strings.TrimSpace(command)
	lower := strings.ToLower(trimmed)
	fields := strings.Fields(lower)
	switch {
	case lower == "":
		return Analysis{Allowed: false, Level: RiskLow, Reason: "empty command"}
	case containsForkBomb(lower):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "fork bomb blocked"}
	case containsRecursiveDelete(fields):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "recursive delete blocked"}
	case containsDiskFormat(lower, fields):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "destructive disk format blocked"}
	case containsCurlPipeShell(lower):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "piped shell execution blocked"}
	case containsBroadOwnershipChange(fields):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "broad permission or ownership change blocked"}
	case containsProcessKill(fields):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "broad process termination blocked"}
	case len(fields) > 0 && fields[0] == "git":
		return a.AnalyzeGit(fields[1:])
	default:
		return Analysis{Allowed: true, Level: RiskLow, Reason: "no obvious risk detected"}
	}
}

func (a *Analyzer) AnalyzeGit(args []string) Analysis {
	if len(args) == 0 {
		return Analysis{Allowed: true, Level: RiskLow, Reason: "git status-like command"}
	}
	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	if isDangerousGit(args) {
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "destructive or shared git operation blocked"}
	}
	if IsSafeGitSubcommand(subcommand) && isSafeGitArgs(subcommand, args[1:]) {
		return Analysis{Allowed: true, Level: RiskLow, Reason: "safe git subcommand"}
	}
	return Analysis{Allowed: false, Level: RiskMedium, Reason: "unsupported git subcommand or arguments"}
}

func isDangerousGit(args []string) bool {
	if len(args) == 0 {
		return false
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "push":
		return true
	case "reset":
		return hasAny(rest, "--hard", "--merge", "--keep")
	case "clean":
		return true
	case "restore":
		return true
	case "checkout":
		return hasAny(rest, "--")
	case "branch":
		return hasAny(rest, "-d", "-D", "--delete")
	case "commit":
		return hasAny(rest, "--amend")
	case "rebase":
		return true
	}
	return hasAny(args, "--force", "--force-with-lease", "-f") && sub == "push"
}

func isSafeGitArgs(subcommand string, args []string) bool {
	switch subcommand {
	case "branch":
		return !hasAny(args, "-d", "-D", "--delete")
	case "worktree":
		if len(args) == 0 {
			return true
		}
		return args[0] == "list"
	case "fetch":
		return !hasAny(args, "--force", "-f")
	default:
		return true
	}
}

func containsRecursiveDelete(fields []string) bool {
	for i, field := range fields {
		if field != "rm" && !strings.HasSuffix(field, "/rm") {
			continue
		}
		for _, arg := range fields[i+1:] {
			if strings.HasPrefix(arg, "-") && strings.Contains(arg, "r") && strings.Contains(arg, "f") {
				return true
			}
			if arg == "/" || arg == "/*" || arg == "." || arg == "./" || arg == ".." || strings.HasPrefix(arg, "~") {
				return true
			}
		}
	}
	return false
}

func containsDiskFormat(lower string, fields []string) bool {
	if strings.Contains(lower, "format c:") || strings.Contains(lower, "mkfs") {
		return true
	}
	return len(fields) > 0 && strings.HasPrefix(fields[0], "diskpart")
}

func containsCurlPipeShell(lower string) bool {
	if !(strings.Contains(lower, "curl ") || strings.Contains(lower, "wget ")) {
		return false
	}
	return strings.Contains(lower, "| sh") || strings.Contains(lower, "| bash") || strings.Contains(lower, "iex") || strings.Contains(lower, "invoke-expression")
}

func containsBroadOwnershipChange(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	cmd := fields[0]
	if cmd != "chmod" && cmd != "chown" && cmd != "chgrp" && !strings.HasSuffix(cmd, "/chmod") && !strings.HasSuffix(cmd, "/chown") && !strings.HasSuffix(cmd, "/chgrp") {
		return false
	}
	return hasAny(fields[1:], "-r", "--recursive") || hasAny(fields[1:], "/", "/*", ".", "./")
}

func containsProcessKill(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	cmd := fields[0]
	if cmd == "killall" || cmd == "pkill" || strings.HasSuffix(cmd, "/killall") || strings.HasSuffix(cmd, "/pkill") {
		return true
	}
	if cmd == "kill" || strings.HasSuffix(cmd, "/kill") {
		return hasAny(fields[1:], "-9", "-kill")
	}
	return false
}

func containsForkBomb(lower string) bool {
	return strings.Contains(lower, ":(){ :|:& };:") || strings.Contains(lower, ":(){:|:&};:")
}

func hasAny(args []string, values ...string) bool {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	for _, arg := range args {
		if _, ok := set[arg]; ok {
			return true
		}
	}
	return false
}
