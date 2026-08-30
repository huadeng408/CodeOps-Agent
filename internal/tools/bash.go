package tools

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"code-agent/internal/sandbox"
	"code-agent/internal/safety"
)

func (e *Executor) executeBash(ctx context.Context, args map[string]any) (ToolResult, error) {
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
	if cdTarget, ok := cdTarget(command); ok && workingDir == "" {
		currentDir, err := e.currentWorkingDir()
		if err != nil {
			return ToolResult{Name: "Bash", Error: err.Error(), ExitCode: 1}, err
		}
		if err := e.SetWorkingDirFrom(currentDir, cdTarget); err != nil {
			return ToolResult{Name: "Bash", Error: err.Error(), ExitCode: 1}, err
		}
		return ToolResult{Name: "Bash", Output: e.WorkingDir()}, nil
	}

	var absDir string
	var err error
	if workingDir != "" {
		absDir, err = workspacePath(e.Root, workingDir)
	} else {
		absDir, err = e.currentWorkingDir()
	}
	if err != nil {
		return ToolResult{Name: "Bash", Error: err.Error(), ExitCode: 1}, err
	}

	timeout := durationArg(args, 30*time.Second, "timeout_seconds", "timeout")
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if runner := e.sandboxRunner(); runner != nil {
		sandboxResult, sandboxErr := runner.Run(runCtx, sandbox.Request{
			Workspace:  e.Root,
			WorkingDir: absDir,
			Command:    command,
		})
		text, truncated := e.TruncateOutput(sandboxResult.Output)
		result := ToolResult{Name: "Bash", Output: text, ExitCode: sandboxResult.ExitCode, Truncated: truncated}
		if sandboxErr != nil {
			result.Error = sandboxErr.Error()
			if result.ExitCode == 0 {
				result.ExitCode = 1
			}
			return result, sandboxErr
		}
		return result, nil
	}

	name, shellArgs := shellCommand(command)
	cmd := exec.CommandContext(runCtx, name, shellArgs...)
	cmd.Dir = absDir
	output, err := cmd.CombinedOutput()
	text, truncated := e.TruncateOutput(string(output))
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
	if cdTarget, ok := cdTarget(command); ok {
		_ = e.SetWorkingDirFrom(absDir, cdTarget)
	}
	return result, nil
}

func cdTarget(command string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) != 2 || strings.ToLower(fields[0]) != "cd" {
		return "", false
	}
	return fields[1], true
}

func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", powershellUTF8Prefix() + command}
	}
	return "sh", []string{"-c", command}
}

func powershellUTF8Prefix() string {
	return "[Console]::InputEncoding=[Text.UTF8Encoding]::new($false); [Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); $OutputEncoding=[Console]::OutputEncoding; "
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
