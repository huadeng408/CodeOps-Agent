//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func canonicalWorkspacePath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errIngestPathNotRegular
	}
	return finalPathByHandle(file)
}

func openVerifiedIngestFile(candidate, canonicalRoot string) (*os.File, error) {
	file, err := os.Open(candidate)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(errIngestPathNotRegular)
	}
	finalPath, err := finalPathByHandle(file)
	if err != nil {
		return fail(err)
	}
	if !pathWithin(canonicalRoot, finalPath) {
		return fail(errIngestPathOutsideWorkspace)
	}
	return file, nil
}

func finalPathByHandle(file *os.File) (string, error) {
	size := uint32(512)
	for {
		buffer := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], size, 0)
		if err != nil {
			return "", err
		}
		if n < size {
			path := windows.UTF16ToString(buffer[:n])
			path = normalizeWindowsHandlePath(path)
			return filepath.Abs(filepath.Clean(path))
		}
		size = n + 1
	}
}

func normalizeWindowsHandlePath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}
