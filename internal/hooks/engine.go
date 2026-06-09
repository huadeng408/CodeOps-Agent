package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	mu       sync.RWMutex
	handlers map[Phase][]Handler
}

func NewEngine() *Engine {
	return &Engine{handlers: map[Phase][]Handler{}}
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
		if strings.TrimSpace(command) == "" {
			return Result{}, nil
		}
		if hook.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(hook.Timeout)*time.Second)
			defer cancel()
		}
		name, args := shellCommand(command)
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
	for needle, value := range replacements {
		command = strings.ReplaceAll(command, needle, value)
	}
	return command
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
