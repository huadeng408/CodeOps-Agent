//go:build !windows

package cli

import (
	"os"
	"path/filepath"
)

func canonicalWorkspacePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
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
	handleInfo, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !handleInfo.Mode().IsRegular() {
		return fail(errIngestPathNotRegular)
	}
	finalPath, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return fail(err)
	}
	finalPath, err = filepath.Abs(finalPath)
	if err != nil {
		return fail(err)
	}
	pathInfo, err := os.Stat(finalPath)
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(handleInfo, pathInfo) {
		return fail(errIngestPathOutsideWorkspace)
	}
	if !pathWithin(canonicalRoot, finalPath) {
		return fail(errIngestPathOutsideWorkspace)
	}
	return file, nil
}
