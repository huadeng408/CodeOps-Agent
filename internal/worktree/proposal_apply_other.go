//go:build !windows

package worktree

import (
	"errors"
	"os"
)

func openApplicationFile(string) (*os.File, error) {
	return nil, errors.New("exclusive proposal application is only available on Windows")
}
