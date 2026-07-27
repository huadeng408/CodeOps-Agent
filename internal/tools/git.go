package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"code-agent/internal/safety"
)

func (e *Executor) executeGit(ctx context.Context, args map[string]any) (ToolResult, error) {
	command, _ := stringArg(args, "command", "subcommand")
	if command == "" {
		command = "status"
	}
	command = strings.ToLower(strings.TrimSpace(command))
	extraArgs := stringSliceArg(args, "args", "arguments")
	analysisArgs := append([]string{command}, lowerArgs(extraArgs)...)
	analysis := safety.NewAnalyzer().AnalyzeGit(analysisArgs)
	if !analysis.Allowed {
		err := fmt.Errorf("blocked git command: %s", analysis.Reason)
		return ToolResult{Name: "Git", Error: err.Error(), ExitCode: 1}, err
	}

	cmdArgs := append([]string{"-C", e.Root, command}, extraArgs...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	output, err := cmd.CombinedOutput()
	text, truncated := e.TruncateOutput(string(output))
	result := ToolResult{Name: "Git", Output: text, Truncated: truncated}
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = exitCodeFromError(err)
		return result, err
	}
	return result, nil
}

func lowerArgs(args []string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = strings.ToLower(strings.TrimSpace(arg))
	}
	return out
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
