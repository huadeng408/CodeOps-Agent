package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"code-agent/internal/safety"
)

func executeGit(ctx context.Context, root string, args map[string]any) (ToolResult, error) {
	command, _ := stringArg(args, "command", "subcommand")
	if command == "" {
		command = "status"
	}
	if !safety.IsSafeGitSubcommand(command) {
		err := fmt.Errorf("unsupported git subcommand %q", command)
		return ToolResult{Name: "Git", Error: err.Error(), ExitCode: 1}, err
	}

	extraArgs := stringSliceArg(args, "args", "arguments")
	cmdArgs := append([]string{"-C", root, command}, extraArgs...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	result := ToolResult{Name: "Git", Output: string(output)}
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = exitCodeFromError(err)
		return result, err
	}
	return result, nil
}

func stringSliceArg(args map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case []string:
			return append([]string(nil), v...)
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	if strings.Contains(err.Error(), "exit status ") {
		return 1
	}
	return 1
}
