package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

func atomicWriteFile(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path is not allowed: %s", path)
		}
		if info.IsDir() {
			return fmt.Errorf("cannot write directory: %s", path)
		}
		if mode == 0 {
			mode = info.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	tmp, err := os.CreateTemp(dir, ".code-agent-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := atomicReplace(tmpName, path); err != nil {
		return err
	}
	removeTemp = false
	if err := syncDirectory(dir); err != nil {
		return err
	}
	return nil
}
