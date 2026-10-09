//go:build !windows

package localidentity

import "os"

func rejectReparsePoint(string) error { return nil }

func protectIdentityPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}
