package tools

import (
	"code-agent/internal/safety"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func (e *Executor) executeFileLifecycle(ctx context.Context, req ToolRequest) (result ToolResult, err error) {
	result.Name = req.Name
	defer func() {
		if err != nil {
			result.Error = err.Error()
			result.ExitCode = 1
		}
	}()
	path, ok := stringArg(req.Arguments, "path")
	if !ok || path == "" {
		result.Error = "path is required"
		return result, errors.New(result.Error)
	}
	abs, err := e.workingFilePath(path)
	if err != nil {
		return result, err
	}
	path, err = workspaceRelativePath(e.Root, abs)
	if err != nil {
		return result, err
	}
	destination, target := "", ""
	parents := []string{e.Root, filepath.Dir(abs)}
	if req.Name == "Rename" {
		destination, ok = stringArg(req.Arguments, "destination")
		if !ok || destination == "" {
			return result, errors.New("rename destination is required")
		}
		target, err = e.workingFilePath(destination)
		if err != nil {
			return result, err
		}
		destination, err = workspaceRelativePath(e.Root, target)
		if err != nil || samePath(abs, target) {
			return result, errors.New("rename source and target must differ")
		}
		if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
			return result, errors.New("rename target must be absent")
		}
		parents = append(parents, filepath.Dir(target))
	}
	for index, parent := range parents {
		parents[index], err = filepath.Abs(parent)
		if err != nil {
			return result, err
		}
	}
	release, err := safety.PinDirectories(parents...)
	if err != nil {
		return result, err
	}
	defer release()
	operation := "delete"
	if req.Name == "Rename" {
		operation = "rename"
	}
	file, err := safety.OpenExclusiveFile(abs, operation)
	if err != nil {
		return result, err
	}
	closed := false
	defer func() {
		if !closed {
			file.Close()
		}
	}()
	before, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	if err != nil || len(before) > 8<<20 {
		return result, errors.New("file lifecycle input unavailable or too large")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if req.Name == "Rename" {
		err = safety.RenameExclusiveFile(file, target)
	} else {
		err = safety.DeleteExclusiveFile(file)
	}
	if err != nil {
		result.OutcomeUnknown = true
		return result, err
	}
	err = file.Close()
	closed = true
	if err != nil {
		result.OutcomeUnknown = true
		return result, err
	}
	if _, statErr := os.Lstat(abs); !os.IsNotExist(statErr) {
		result.OutcomeUnknown = true
		return result, errors.New("file lifecycle source outcome requires reconciliation")
	}
	after := ""
	if req.Name == "Rename" {
		data, readErr := os.ReadFile(target)
		if readErr != nil || string(data) != string(before) {
			result.OutcomeUnknown = true
			return result, errors.New("rename target outcome requires reconciliation")
		}
		after = string(data)
	}
	beforeExists, afterExists := true, req.Name == "Rename"
	result.Output = "file lifecycle completed"
	result.Changes = []Change{{Path: path, Destination: destination, Before: string(before), After: after, BeforeExists: &beforeExists, AfterExists: &afterExists}}
	return result, nil
}
