package safety

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const maxGitArgumentLength = 8192

// harnessControlEnvironmentKeys are process-level controls owned by the
// Harness/orchestrator boundary. Host tools must not inherit them because a
// child could select a provider, reload a provider file, or change Harness
// worktree behavior outside the parent authorization path.
var harnessControlEnvironmentKeys = map[string]struct{}{
	"CODE_AGENT_PROVIDER_CONFIG":          {},
	"CODE_AGENT_PROVIDER_PROFILE":         {},
	"CODE_AGENT_REQUIRE_HARNESS_WORKTREE": {},
	"LLM_PROVIDER":                        {},
	"MODEL_FAST":                          {},
	"THINKING_ENABLED":                    {},
}

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
	case containsPowerShellDestructive(lower):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "destructive PowerShell execution blocked"}
	case containsBroadOwnershipChange(fields):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "broad permission or ownership change blocked"}
	case containsProcessKill(fields):
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "broad process termination blocked"}
	case len(fields) > 0:
		if gitIndex := findGitExecutable(fields); gitIndex >= 0 {
			gitArgs := fields[gitIndex+1:]
			if end := gitShellBoundary(gitArgs); end >= 0 {
				gitArgs = gitArgs[:end]
			}
			return a.AnalyzeGit(gitArgs)
		}
		return Analysis{Allowed: true, Level: RiskLow, Reason: "no obvious risk detected"}
	default:
		return Analysis{Allowed: true, Level: RiskLow, Reason: "no obvious risk detected"}
	}
}

func (a *Analyzer) AnalyzeGit(args []string) Analysis {
	if len(args) == 0 {
		return Analysis{Allowed: true, Level: RiskLow, Reason: "git status-like command"}
	}
	normalized := make([]string, len(args))
	for i, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" && i == 0 {
			return Analysis{Allowed: false, Level: RiskMedium, Reason: "git subcommand is empty"}
		}
		if len(arg) > maxGitArgumentLength || strings.IndexByte(arg, 0) >= 0 || containsControl(arg) {
			return Analysis{Allowed: false, Level: RiskHigh, Reason: "malformed git argument"}
		}
		normalized[i] = strings.ToLower(trimmed)
	}
	subcommand := normalized[0]
	if containsGitEscape(normalized) {
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "git repository escape or external hook blocked"}
	}
	if isDangerousGit(normalized) {
		return Analysis{Allowed: false, Level: RiskHigh, Reason: "destructive or shared git operation blocked"}
	}
	if IsSafeGitSubcommand(subcommand) && isSafeGitArgs(subcommand, normalized[1:]) {
		return Analysis{Allowed: true, Level: RiskLow, Reason: "safe git subcommand"}
	}
	return Analysis{Allowed: false, Level: RiskMedium, Reason: "unsupported git subcommand or arguments"}
}

// findGitExecutable locates a git executable token at the command position,
// including git.exe, absolute paths, and a small set of shell wrappers. It
// deliberately does not scan arbitrary prose for the word "git": prompts sent
// to the agent commonly mention Git without being shell commands.
func findGitExecutable(fields []string) int {
	if len(fields) == 0 {
		return -1
	}
	indices := []int{0}
	switch fields[0] {
	case "env", "command", "sudo":
		for i := 1; i < len(fields) && i <= 8; i++ {
			if fields[i] == "--" {
				indices = append(indices, i+1)
				break
			}
			if strings.Contains(fields[i], "=") || strings.HasPrefix(fields[i], "-") {
				continue
			}
			indices = append(indices, i)
			break
		}
	}
	for _, i := range indices {
		if i < 0 || i >= len(fields) {
			continue
		}
		field := fields[i]
		field = strings.Trim(field, "\"'")
		field = strings.ReplaceAll(field, "\\", "/")
		base := field
		if slash := strings.LastIndexByte(base, '/'); slash >= 0 {
			base = base[slash+1:]
		}
		if base == "git" || base == "git.exe" {
			return i
		}
	}
	return -1
}

func gitShellBoundary(args []string) int {
	for i, arg := range args {
		switch arg {
		case ";", "&&", "||", "|":
			return i
		}
	}
	return -1
}

// HardenedGitArgs constructs every Git invocation issued by the Harness. The
// command-level settings neutralize repository configuration that could invoke
// aliases, hooks, external diff programs, fsmonitor helpers, or credential
// helpers. The caller still supplies the validated user arguments unchanged.
func HardenedGitArgs(root, command string, args []string) []string {
	command = strings.ToLower(strings.TrimSpace(command))
	hooksPath := filepath.Join(os.DevNull, "codeops-agent-hooks-disabled")
	result := []string{
		"-c", "alias." + command + "=",
		"-c", "core.hooksPath=" + hooksPath,
		"-c", "diff.external=",
		"-c", "core.fsmonitor=false",
		"-c", "credential.helper=",
		"-c", "protocol.ext.allow=never",
		"-c", "safe.directory=" + root,
	}
	if strings.TrimSpace(root) != "" {
		result = append(result, "-C", root)
	}
	result = append(result, command)
	if command == "diff" {
		// diff.external is an executable setting; an empty -c value still
		// makes Git try to spawn it. This flag is the reliable per-invocation
		// opt-out across Git versions and platforms.
		result = append(result, "--no-ext-diff")
	}
	return append(result, args...)
}

func containsGitEscape(args []string) bool {
	for _, arg := range args[1:] {
		if arg == "-c" || arg == "-c=" || strings.HasPrefix(arg, "-c=") || strings.HasPrefix(arg, "-c") && len(arg) > 2 {
			return true
		}
		if arg == "-C" || strings.HasPrefix(arg, "-c") && len(arg) > 2 {
			return true
		}
		if arg == "--ext-diff" || arg == "--no-ext-diff" || arg == "--external-diff" {
			return true
		}
		for _, prefix := range []string{
			"--git-dir", "--work-tree", "--super-prefix", "--exec-path", "--config",
			"--config-env", "--upload-pack", "--receive-pack", "--ssh-command",
			"--index-file", "--object-directory", "--alternate-objects", "--namespace",
			"--pathspec-from-file", "--pathspec-file-nul", "--output",
		} {
			if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
				return true
			}
		}
		if arg == "--no-index" {
			return true
		}
	}
	return false
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
		return isReadOnlyBranchArgs(args)
	case "worktree":
		if len(args) == 0 {
			return true
		}
		return args[0] == "list"
	case "fetch":
		return isBoundedFetchArgs(args)
	default:
		return true
	}
}

// isReadOnlyBranchArgs keeps the generic Git tool from changing refs or
// branch metadata. Positional values are accepted only after a read-only
// selector such as --list/--contains, where Git treats them as patterns or
// revisions rather than branch names to create.
func isReadOnlyBranchArgs(args []string) bool {
	if len(args) == 0 {
		return true
	}
	if hasAny(args,
		"-d", "-D", "-f", "--delete", "--move", "--copy", "-m", "-M", "-c", "-C",
		"--edit-description", "--set-upstream-to", "--unset-upstream", "--track",
		"--no-track", "--set-branch-description", "--create-reflog", "--force",
	) {
		return false
	}
	readSelector := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--list" || arg == "-l" || arg == "--show-current":
			readSelector = true
		case arg == "--all" || arg == "-a" || arg == "--remotes" || arg == "-r":
			readSelector = true
		case arg == "--verbose" || arg == "-v" || arg == "-vv":
			readSelector = true
		case arg == "--merged" || arg == "--no-merged" || arg == "--contains" || arg == "--no-contains":
			readSelector = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		case arg == "--points-at", arg == "--format", arg == "--sort":
			readSelector = true
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return false
			}
			i++
		case arg == "--column" || arg == "--color" || arg == "--abbrev":
			readSelector = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		case strings.HasPrefix(arg, "--merged=") || strings.HasPrefix(arg, "--no-merged=") ||
			strings.HasPrefix(arg, "--contains=") || strings.HasPrefix(arg, "--no-contains=") ||
			strings.HasPrefix(arg, "--points-at=") || strings.HasPrefix(arg, "--format=") ||
			strings.HasPrefix(arg, "--sort=") || strings.HasPrefix(arg, "--column=") ||
			strings.HasPrefix(arg, "--color=") ||
			strings.HasPrefix(arg, "--abbrev="):
			readSelector = true
		case arg == "--no-color" || arg == "--no-column" || arg == "--no-abbrev" || arg == "--omit-empty":
			readSelector = true
		case arg == "--":
			return readSelector
		case strings.HasPrefix(arg, "-"):
			return false
		case !readSelector:
			return false
		}
	}
	return true
}

// isBoundedFetchArgs permits the ordinary remote-tracking update used for
// inspection while rejecting options that delete refs, rewrite arbitrary
// refspecs, update the checked-out branch, or invoke remote-side commands.
func isBoundedFetchArgs(args []string) bool {
	safeOptions := map[string]struct{}{
		"-4": {}, "-6": {}, "-n": {}, "-q": {}, "-v": {},
		"--all": {}, "--auto-maintenance": {}, "--dry-run": {},
		"--ipv4": {}, "--ipv6": {}, "--keep": {}, "--multiple": {},
		"--no-auto-maintenance": {}, "--no-progress": {}, "--no-tags": {},
		"--no-write-fetch-head": {}, "--porcelain": {}, "--progress": {},
		"--quiet": {}, "--tags": {}, "--verbose": {},
	}
	repositorySeen := false
	optionsEnded := false
	for _, arg := range args {
		if !optionsEnded && arg == "--" {
			optionsEnded = true
			continue
		}
		if !optionsEnded && strings.HasPrefix(arg, "-") {
			if strings.HasPrefix(arg, "--force") {
				return false
			}
			if _, ok := safeOptions[arg]; !ok {
				return false
			}
			continue
		}
		if !repositorySeen {
			repositorySeen = true
			continue
		}
		// A colon refspec controls the destination ref. A leading plus forces
		// an update. Both exceed the generic Git tool's read-only boundary.
		if strings.Contains(arg, ":") || strings.HasPrefix(arg, "+") {
			return false
		}
	}
	return true
}

func containsRecursiveDelete(fields []string) bool {
	for i, field := range fields {
		if field != "rm" && !strings.HasSuffix(field, "/rm") {
			continue
		}
		recursive, force := false, false
		for _, arg := range fields[i+1:] {
			if strings.HasPrefix(arg, "-") && strings.Contains(arg, "r") && strings.Contains(arg, "f") {
				return true
			}
			if strings.HasPrefix(arg, "-") {
				recursive = recursive || strings.Contains(arg, "r")
				force = force || strings.Contains(arg, "f")
			}
			if arg == "/" || arg == "/*" || arg == "." || arg == "./" || arg == ".." || strings.HasPrefix(arg, "~") {
				return true
			}
		}
		if recursive && force {
			return true
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
	return strings.Contains(lower, "| sh") || strings.Contains(lower, "|sh") || strings.Contains(lower, "| bash") || strings.Contains(lower, "|bash") || strings.Contains(lower, "iex") || strings.Contains(lower, "invoke-expression")
}

func containsPowerShellDestructive(lower string) bool {
	return (strings.Contains(lower, "remove-item") && strings.Contains(lower, "-recurse") && strings.Contains(lower, "-force")) ||
		strings.Contains(lower, "invoke-expression") || strings.Contains(lower, "invoke-command -scriptblock") ||
		strings.Contains(lower, "set-executionpolicy")
}

// ScrubEnvironment removes ambient values that commonly carry credentials or
// Harness internals before a host shell process is started. Explicit command
// arguments remain visible to the command and are governed by the analyzer.
func ScrubEnvironment(env []string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(key))
		if _, blocked := harnessControlEnvironmentKeys[upper]; blocked {
			continue
		}
		if upper == "" || strings.HasPrefix(upper, "DSH_") || strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") {
			continue
		}
		result = append(result, entry)
	}
	return result
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

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// ScrubGitEnvironment removes ambient Git controls that can redirect a
// command to another repository, index, transport or executable. Git is
// invoked with explicit -C by the Harness, so these variables are never
// needed for a tool call.
func ScrubGitEnvironment(env []string) []string {
	blocked := func(key string) bool {
		upper := strings.ToUpper(strings.TrimSpace(key))
		if strings.HasPrefix(upper, "GIT_") {
			return true
		}
		return false
	}
	result := make([]string, 0, len(env)+1)
	for _, entry := range ScrubEnvironment(env) {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || blocked(key) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "GIT_TERMINAL_PROMPT") {
			continue
		}
		result = append(result, entry)
	}
	result = append(result,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_LAZY_FETCH=1",
	)
	return result
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
