package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunExportsBuiltInMetadataWithoutPromptBodies(t *testing.T) {
	output := filepath.Join(t.TempDir(), ".agent", "skills.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--output", output}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("run failed with %d: %s", exitCode, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Skills) < 40 {
		t.Fatalf("expected at least 40 skills, got %d", len(manifest.Skills))
	}
	for _, skill := range manifest.Skills {
		if _, leaked := skill["prompt"]; leaked {
			t.Fatalf("manifest leaked prompt body for %v", skill["name"])
		}
	}
	if stdout.String() != output+"\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}
