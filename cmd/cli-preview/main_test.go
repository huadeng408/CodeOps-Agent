package main

import (
	"errors"
	"testing"
)

func TestPreviewWorkspaceUsesCurrentDirectory(t *testing.T) {
	got := previewWorkspace(func() (string, error) {
		return `D:\work\repository`, nil
	})
	if got != `D:\work\repository` {
		t.Fatalf("previewWorkspace() = %q", got)
	}
}

func TestPreviewWorkspaceFallsBackWhenCurrentDirectoryIsUnavailable(t *testing.T) {
	got := previewWorkspace(func() (string, error) {
		return "", errors.New("cwd unavailable")
	})
	if got != "." {
		t.Fatalf("previewWorkspace() = %q, want .", got)
	}
}
