package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type bundleManifest struct {
	SchemaVersion int               `json:"schema_version"`
	SourceCommit  string            `json:"source_commit"`
	SourceFiles   map[string]string `json:"source_files"`
	Files         map[string]string `json:"files"`
	Components    json.RawMessage   `json:"components"`
}

func verifyBundle(directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return errors.New("cannot open the product directory")
	}
	defer root.Close()
	file, err := root.Open("bundle-manifest.json")
	if err != nil {
		return errors.New("bundle manifest is missing or unreadable; extract the complete CodeOps-Agent distribution")
	}
	defer file.Close()
	var manifest bundleManifest
	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.SchemaVersion != 1 || len(manifest.Files) == 0 {
		return errors.New("bundle manifest is invalid")
	}
	for name, checksum := range manifest.Files {
		if !filepath.IsLocal(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:") || name == "." || len(checksum) != 64 {
			return errors.New("bundle manifest has an invalid component identity")
		}
		file, err := root.Open(name)
		if err != nil {
			return errors.New("bundle component is missing or unreadable")
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != checksum {
			return errors.New("bundle component checksum mismatch; extract a verified distribution")
		}
	}
	if err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil || !entry.Type().IsRegular() {
			return errors.New("unsafe file")
		}
		if filepath.ToSlash(relative) != "bundle-manifest.json" {
			if _, exists := manifest.Files[filepath.ToSlash(relative)]; !exists {
				return errors.New("unlisted file")
			}
		}
		return nil
	}); err != nil {
		return errors.New("bundle contains an unlisted or unsafe file; extract a verified distribution")
	}
	for _, required := range []string{"CodeOps-Agent.exe", "bin/code-agent-server.exe", "python/python.exe", "python/python313._pth", "python/python313.zip", "git/cmd/git.exe", "app/orchestrator/server.py", "configs/local-core.yaml", "frontend/dist/index.html"} {
		if _, exists := manifest.Files[required]; !exists {
			return errors.New("bundle manifest is missing a required product component")
		}
	}
	return nil
}

func main() {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot locate the product executable")
		os.Exit(1)
	}
	if err := verifyBundle(filepath.Dir(executable)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := runProduct(filepath.Dir(executable)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
