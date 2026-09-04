package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"code-agent/internal/safety"
	"code-agent/internal/sandbox"
)

func (e *Executor) executeGit(ctx context.Context, args map[string]any) (ToolResult, error) {
	command, _ := stringArg(args, "command", "subcommand")
	if command == "" {
		command = "status"
	}
	command = strings.ToLower(strings.TrimSpace(command))
	extraArgs, argErr := stringSliceArg(args, "args", "arguments")
	if argErr != nil {
		result := ToolResult{Name: "Git", Error: argErr.Error(), ExitCode: 1}
		return result, argErr
	}
	analysisArgs := append([]string{command}, lowerArgs(extraArgs)...)
	analysis := safety.NewAnalyzer().AnalyzeGit(analysisArgs)
	if !analysis.Allowed {
		err := fmt.Errorf("blocked git command: %s", analysis.Reason)
		return ToolResult{Name: "Git", Error: err.Error(), ExitCode: 1}, err
	}

	if runner := e.sandboxRunner(); runner != nil {
		requestArgs := append([]string{"-c", "safe.directory=/workspace", "-C", "/workspace", command}, extraArgs...)
		sandboxResult, sandboxErr := runner.Run(ctx, sandbox.Request{
			Workspace:  e.Root,
			WorkingDir: e.Root,
			Program:    "git",
			Args:       requestArgs,
		})
		result := ToolResult{Name: "Git", Output: sandboxResult.Output, ExitCode: sandboxResult.ExitCode}
		if sandboxErr != nil {
			result.Error = sandboxErr.Error()
			if result.ExitCode == 0 {
				result.ExitCode = 1
			}
			return result, sandboxErr
		}
		return result, nil
	}

	cmdArgs := append([]string{"-C", e.Root, command}, extraArgs...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = safety.ScrubGitEnvironment(os.Environ())
	output, err := cmd.CombinedOutput()
	result := ToolResult{Name: "Git", Output: string(output)}
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

func stringSliceArg(args map[string]any, keys ...string) ([]string, error) {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case []string:
			return append([]string(nil), v...), nil
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("git arguments must be strings")
				}
				out = append(out, s)
			}
			return out, nil
		default:
			return nil, fmt.Errorf("git arguments must be strings")
		}
	}
	return nil, nil
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
