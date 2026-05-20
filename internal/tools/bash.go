package tools

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"code-agent/internal/safety"
)

func executeBash(ctx context.Context, root string, args map[string]any) (ToolResult, error) {
	command, ok := stringArg(args, "command", "cmd")
	if !ok || command == "" {
		return ToolResult{Name: "Bash", Error: "command is required", ExitCode: 1}, fmt.Errorf("command is required")
	}

	analysis := safety.NewAnalyzer().AnalyzeCommand(command)
	if !analysis.Allowed {
		err := fmt.Errorf("blocked command: %s", analysis.Reason)
		return ToolResult{Name: "Bash", Error: err.Error(), ExitCode: 1}, err
	}

	workingDir, _ := stringArg(args, "cwd", "working_dir")
	absDir, err := workspacePath(root, workingDir)
	if err != nil {
		return ToolResult{Name: "Bash", Error: err.Error(), ExitCode: 1}, err
	}

	timeout := durationArg(args, 30*time.Second, "timeout_seconds", "timeout")
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name, shellArgs := shellCommand(command)
	cmd := exec.CommandContext(runCtx, name, shellArgs...)
	cmd.Dir = absDir
	output, err := cmd.CombinedOutput()
	text, truncated := normalizeOutput(string(output), 50_000)
	result := ToolResult{Name: "Bash", Output: text, Truncated: truncated}

	if runCtx.Err() == context.DeadlineExceeded {
		result.Error = "command timed out"
		result.ExitCode = 124
		return result, runCtx.Err()
	}
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = exitCodeFromError(err)
		return result, err
	}
	return result, nil
}

func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", command}
	}
	return "sh", []string{"-c", command}
}

func durationArg(args map[string]any, fallback time.Duration, keys ...string) time.Duration {
	for _, key := range keys {
		value, ok := args[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case int:
			if v > 0 {
				return time.Duration(v) * time.Second
			}
		case int64:
			if v > 0 {
				return time.Duration(v) * time.Second
			}
		case float64:
			if v > 0 {
				return time.Duration(v * float64(time.Second))
			}
		case string:
			parsed, err := time.ParseDuration(v)
			if err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return fallback
}
