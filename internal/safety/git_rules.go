package safety

import "strings"

var safeGitSubcommands = map[string]struct{}{
	"status":   {},
	"diff":     {},
	"log":      {},
	"show":     {},
	"branch":   {},
	"fetch":    {},
	"worktree": {},
}

func IsSafeGitSubcommand(command string) bool {
	_, ok := safeGitSubcommands[strings.ToLower(strings.TrimSpace(command))]
	return ok
}
