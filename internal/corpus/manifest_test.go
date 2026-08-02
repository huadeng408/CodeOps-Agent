package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

const manifestYAML = `
schema_version: "1"
generation: "techdocs-2026-07-30-v1"
sources:
  - source_id: "python"
    repository_url: "https://github.com/python/cpython"
    source_commit: "0123456789abcdef0123456789abcdef01234567"
    include_paths: ["Doc/"]
    exclude_paths: ["Doc/includes"]
    license_spdx: "PSF-2.0"
    license_path: "LICENSE"
    license_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    allowed_formats: ["rst"]
    expected_documents: 100
    loader_user: 1
    language: "en"
`

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadManifestParsesSource(t *testing.T) {
	manifest, err := LoadManifest(writeManifest(t, manifestYAML))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != "1" || manifest.Generation != "techdocs-2026-07-30-v1" {
		t.Fatalf("manifest header = %+v", manifest)
	}
	if len(manifest.Sources) != 1 || manifest.Sources[0].SourceID != "python" {
		t.Fatalf("sources = %+v", manifest.Sources)
	}
	source := manifest.Sources[0]
	if source.RepositoryURL != "https://github.com/python/cpython" || source.ExpectedDocuments != 100 || source.LoaderUser != 1 {
		t.Fatalf("source = %+v", source)
	}
}

func TestLoadManifestRejectsUnsupportedSchema(t *testing.T) {
	content := "schema_version: \"2\"\ngeneration: \"g\"\nsources: []\n"
	_, err := LoadManifest(writeManifest(t, content))
	if err == nil {
		t.Fatal("expected schema version rejection")
	}
}

func TestLoadManifestRejectsEmptySources(t *testing.T) {
	content := "schema_version: \"1\"\ngeneration: \"g\"\nsources: []\n"
	_, err := LoadManifest(writeManifest(t, content))
	if err == nil {
		t.Fatal("expected empty sources rejection")
	}
}

func TestLoadManifestRejectsEmptyIncludePaths(t *testing.T) {
	content := `
schema_version: "1"
generation: "g"
sources:
  - source_id: "python"
    repository_url: "https://github.com/python/cpython"
    source_commit: "0123456789abcdef0123456789abcdef01234567"
    include_paths: []
`
	_, err := LoadManifest(writeManifest(t, content))
	if err == nil || !contains(err.Error(), "include_paths") {
		t.Fatalf("expected include_paths rejection, got %v", err)
	}
}

func TestSourceIncludesExcludeWinsOverInclude(t *testing.T) {
	source := SourceSpec{
		IncludePaths: []string{"Doc/"},
		ExcludePaths: []string{"Doc/includes"},
	}
	if !source.Includes("Doc/library.rst") {
		t.Fatal("Doc/library.rst should be included")
	}
	if source.Includes("Doc/includes/foo.rst") {
		t.Fatal("Doc/includes/foo.rst must be excluded")
	}
	if source.Includes("Doc/includes") {
		t.Fatal("exact excluded dir must be excluded")
	}
}

func TestSourceIncludesRejectsTraversal(t *testing.T) {
	source := SourceSpec{IncludePaths: []string{"Doc/"}}
	for _, path := range []string{"../etc/passwd", "/etc/passwd", "Doc/../README"} {
		if source.Includes(path) {
			t.Fatalf("traversal path %q must be rejected", path)
		}
	}
}

func TestSourceIncludesNestedDirectoryPattern(t *testing.T) {
	source := SourceSpec{IncludePaths: []string{"content/en/docs/"}}
	if !source.Includes("content/en/docs/concepts/overview.md") {
		t.Fatal("nested dir include should match")
	}
	if source.Includes("content/zh/docs/concepts/overview.md") {
		t.Fatal("other language tree must not match")
	}
}

func TestSourceAllowedFormat(t *testing.T) {
	source := SourceSpec{AllowedFormats: []string{"rst", "md"}}
	for _, ok := range []bool{source.Allowed("rst"), source.Allowed(".rst"), source.Allowed("MD"), source.Allowed("md")} {
		if !ok {
			t.Fatalf("expected rst/md allowed: %v", ok)
		}
	}
	if source.Allowed("pdf") || source.Allowed("html") {
		t.Fatal("pdf/html must not be allowed")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
