package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"code-agent/internal/sandbox"
)

type Engine struct {
	mu       sync.RWMutex
	handlers map[Phase][]Handler
	runner   sandbox.Runner
	root     string
}

func NewEngine() *Engine {
	return &Engine{handlers: map[Phase][]Handler{}}
}

// SetSandbox shares the Harness execution boundary with command Hooks.
func (e *Engine) SetSandbox(runner sandbox.Runner, root string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.runner, e.root = runner, root
}

func (e *Engine) Register(phase Phase, handler Handler) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.handlers[phase] = append(e.handlers[phase], handler)
}

func (e *Engine) RegisterCommandHook(hook CommandHook) {
	e.Register(hook.Phase, func(ctx context.Context, hookCtx Context) (Result, error) {
		if hook.Matcher != "" && hook.Matcher != hookCtx.ToolName {
			return Result{}, nil
		}
		command := expandCommand(hook.Command, hookCtx)
		if (strings.TrimSpace(command) != "" && hook.Program != "") || (hook.Program == "" && len(hook.Args) != 0) {
			return Result{Cancel: true}, errors.New("hook requires exactly one command or program with literal arguments")
		}
		if strings.TrimSpace(command) == "" && hook.Program == "" {
			return Result{}, nil
		}
		args := make([]string, len(hook.Args))
		for index, value := range hook.Args {
			args[index] = expandCommand(value, hookCtx)
		}
		if hook.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(hook.Timeout)*time.Second)
			defer cancel()
		}
		e.mu.RLock()
		runner, root := e.runner, e.root
		e.mu.RUnlock()
		if runner != nil {
			workingDir := hook.WorkDir
			if workingDir == "" {
				workingDir = root
			}
			outcome, err := runner.Run(ctx, sandbox.Request{Workspace: root, WorkingDir: workingDir, OperationID: hookCtx.OperationID, DependencyOperationID: hookCtx.DependencyOperationID, Command: command, Program: hook.Program, Args: args})
			result := Result{Message: strings.TrimSpace(outcome.Output), Values: map[string]any{
				"output": strings.TrimSpace(outcome.Output), "exit_code": outcome.ExitCode,
				"truncated": outcome.Truncated, "runtime_id": outcome.RuntimeID, "stopped": outcome.Stopped,
				"outcome_unknown": errors.Is(err, sandbox.ErrSandboxOutcomeUnknown),
			}}
			if err == nil && outcome.ExitCode != 0 {
				err = fmt.Errorf("exit status %d", outcome.ExitCode)
			}
			if err != nil {
				result.Cancel = true
				if result.Message == "" {
					result.Message = err.Error()
				}
				return result, fmt.Errorf("hook command failed: %w", err)
			}
			return result, nil
		}
		name := hook.Program
		if name == "" {
			name, args = shellCommand(command)
		}
		cmd := exec.CommandContext(ctx, name, args...)
		if hook.WorkDir != "" {
			cmd.Dir = hook.WorkDir
		}
		output, err := cmd.CombinedOutput()
		result := Result{
			Values: map[string]any{
				"output": strings.TrimSpace(string(output)),
			},
			Message: strings.TrimSpace(string(output)),
		}
		if err != nil {
			result.Cancel = true
			if result.Message == "" {
				result.Message = err.Error()
			}
			return result, fmt.Errorf("hook command failed: %w", err)
		}
		return result, nil
	})
}

func (e *Engine) Run(ctx context.Context, phase Phase, hook Context) ([]Result, error) {
	e.mu.RLock()
	handlers := append([]Handler(nil), e.handlers[phase]...)
	e.mu.RUnlock()

	results := make([]Result, 0, len(handlers))
	for _, handler := range handlers {
		result, err := handler(ctx, hook)
		results = append(results, result)
		if id, _ := result.Values["runtime_id"].(string); id != "" && hook.RecordRuntime != nil {
			if persistErr := hook.RecordRuntime(phase, result); persistErr != nil {
				return results, errors.Join(err, fmt.Errorf("%w: Hook runtime receipt was not persisted", sandbox.ErrSandboxOutcomeUnknown))
			}
		}
		if err != nil {
			return results, err
		}
		if result.Cancel {
			break
		}
	}
	return results, nil
}

func expandCommand(command string, hook Context) string {
	paramsJSON, _ := json.Marshal(hook.Payload)
	replacements := map[string]string{
		"${SESSION_ID}":  hook.SessionID,
		"${TOOL_NAME}":   hook.ToolName,
		"${TOOL_PARAMS}": string(paramsJSON),
	}
	for key, value := range hook.Payload {
		replacements["${"+strings.ToUpper(key)+"}"] = fmt.Sprint(value)
	}
	for key, value := range hook.Metadata {
		replacements["${"+strings.ToUpper(key)+"}"] = value
	}
	pairs := make([]string, 0, len(replacements)*2)
	for needle, value := range replacements {
		pairs = append(pairs, needle, value)
	}
	return strings.NewReplacer(pairs...).Replace(command)
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
