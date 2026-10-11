package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadInstructionsPrefersCanonicalBodyOnce(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	for name, body := range map[string]string{"AGENTS.md": "canonical rules", "AGENT.md": "entry link"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := LoadInstructions(root, root)
	if err != nil || len(sources) != 1 || sources[0].Path != filepath.Join(root, "AGENTS.md") || sources[0].Content != "canonical rules" {
		t.Fatalf("canonical instructions not loaded once: sources=%+v err=%v", sources, err)
	}
}

func TestLoadInstructionsKeepsLegacyDirectoryLayers(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	workingDir := filepath.Join(root, "sub")
	for directory, body := range map[string]string{filepath.Join(home, ".agent"): "global", root: "project", workingDir: "working"} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "AGENT.md"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := LoadInstructions(root, workingDir)
	if err != nil || len(sources) != 3 {
		t.Fatalf("legacy instruction layers: sources=%+v err=%v", sources, err)
	}
	for i, want := range []string{"global", "project", "working"} {
		if sources[i].Content != want {
			t.Fatalf("layer %d = %q, want %q", i, sources[i].Content, want)
		}
	}
}

func TestLoadInstructionsDoesNotHideCanonicalReadFailure(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(root, "AGENTS.md"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENT.md"), []byte("legacy rules"), 0600); err != nil {
		t.Fatal(err)
	}
	if sources, err := LoadInstructions(root, root); err == nil {
		t.Fatalf("canonical read failure silently used legacy: %+v", sources)
	}
}
