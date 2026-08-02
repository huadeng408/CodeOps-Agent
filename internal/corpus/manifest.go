// Package corpus implements the official technical corpus loader: staged
// checkout, license gate, pilot sampling and idempotent checkpointing.
package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Manifest mirrors corpus/manifest.schema.json (schema_version "1").
type Manifest struct {
	SchemaVersion string       `yaml:"schema_version"`
	Generation    string       `yaml:"generation"`
	Sources       []SourceSpec `yaml:"sources"`
}

// SourceSpec is one pinned source in the manifest.
type SourceSpec struct {
	SourceID         string   `yaml:"source_id"`
	RepositoryURL    string   `yaml:"repository_url"`
	SourceCommit     string   `yaml:"source_commit"`
	IncludePaths     []string `yaml:"include_paths"`
	ExcludePaths     []string `yaml:"exclude_paths"`
	LicenseSPDX      string   `yaml:"license_spdx"`
	LicensePath      string   `yaml:"license_path"`
	LicenseSHA256    string   `yaml:"license_sha256"`
	AllowedFormats   []string `yaml:"allowed_formats"`
	ExpectedDocuments int     `yaml:"expected_documents"`
	LoaderUser       uint     `yaml:"loader_user"`
	Language         string   `yaml:"language"`
}

// LoadManifest parses and shape-validates a YAML manifest.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if manifest.SchemaVersion != "1" {
		return nil, fmt.Errorf("manifest %s: unsupported schema_version %q", path, manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.Generation) == "" {
		return nil, fmt.Errorf("manifest %s: generation is required", path)
	}
	if len(manifest.Sources) == 0 {
		return nil, fmt.Errorf("manifest %s: at least one source is required", path)
	}
	for i, source := range manifest.Sources {
		if strings.TrimSpace(source.SourceID) == "" {
			return nil, fmt.Errorf("manifest %s: source %d has empty source_id", path, i)
		}
		if len(source.IncludePaths) == 0 {
			return nil, fmt.Errorf("manifest %s: source %s has no include_paths", path, source.SourceID)
		}
	}
	return &manifest, nil
}

// SourceGate converts a manifest source into the gate evidence used by the
// license/pinning gate.
func (s SourceSpec) SourceGate() SourceGate {
	return SourceGate{
		SourceID:      s.SourceID,
		SourceCommit:  s.SourceCommit,
		LicensePath:   s.LicensePath,
		LicenseSHA256: s.LicenseSHA256,
		LicenseSPDX:   s.LicenseSPDX,
	}
}

// Includes reports whether a repo-relative path is selected by the manifest
// include/exclude rules. Paths are matched on slash-separated prefixes;
// exclude wins over include. Absolute paths and traversal segments are always
// rejected.
func (s SourceSpec) Includes(relPath string) bool {
	cleaned := filepath.ToSlash(strings.TrimPrefix(relPath, "./"))
	if strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "../") || strings.Contains(cleaned, "..\\") {
		return false
	}
	for _, pattern := range s.ExcludePaths {
		if pathMatches(pattern, cleaned) {
			return false
		}
	}
	for _, pattern := range s.IncludePaths {
		if pathMatches(pattern, cleaned) {
			return true
		}
	}
	return false
}

// pathMatches matches a glob-style pattern (supports trailing /** and *
// wildcards) against a slash-separated path. A plain directory pattern also
// matches its entire subtree.
func pathMatches(pattern, path string) bool {
	pattern = filepath.ToSlash(pattern)
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	if pattern == path {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	// A plain directory pattern matches the directory and its subtree.
	if !strings.Contains(pattern, "*") && strings.HasPrefix(path, pattern+"/") {
		return true
	}
	if strings.Contains(pattern, "*") {
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
	}
	return false
}

// Allowed reports whether the document extension is in the manifest's
// allowed_formats.
func (s SourceSpec) Allowed(ext string) bool {
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	for _, format := range s.AllowedFormats {
		if strings.TrimPrefix(strings.ToLower(format), ".") == ext {
			return true
		}
	}
	return false
}
