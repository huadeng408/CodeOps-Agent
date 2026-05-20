package tools

import (
	"context"
	"errors"
)

func executeBash(_ context.Context, _ string, _ map[string]any) (ToolResult, error) {
	err := errors.New("bash execution is not wired in the skeleton yet")
	return ToolResult{Name: "Bash", Error: err.Error(), ExitCode: 1}, err
}
