//go:build !windows

package safety

import (
	"errors"
	"os"
)

func RejectReparsePoint(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlink is not allowed")
	}
	return nil
}

func ProtectPrivatePath(path string) error {
	if err := RejectReparsePoint(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Chmod(path, 0700)
	}
	return os.Chmod(path, 0600)
}

// shortcut: external product worktree preparation is Windows-first; admit Unix
// only after native Git path resolution has a verified directory pin contract.
func PinDirectories(...string) (func(), error) {
	return nil, errors.New("native Git directory pinning is not admitted on this platform")
}

func PinReadOnlyFiles(...string) (func(), error) { return PinDirectories() }
