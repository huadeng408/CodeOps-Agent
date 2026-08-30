package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewManagerRegistersCommitSkill(t *testing.T) {
	manager := NewManager()
	skill, ok := manager.Get("commit")
	if !ok {
		t.Fatal("expected the commit skill to be registered in NewManager")
	}
	if skill.Name != "commit" {
		t.Fatalf("unexpected skill name: %q", skill.Name)
	}
	if skill.Prompt == "" {
		t.Fatal("commit skill must carry a prompt template")
	}
	if skill.Description == "" {
		t.Fatal("commit skill must carry a description")
	}
}

func TestNewManagerRegistersAllBuiltins(t *testing.T) {
	manager := NewManager()
	for _, name := range []string{"init", "review", "security", "commit"} {
		if _, ok := manager.Get(name); !ok {
			t.Errorf("expected built-in skill %q to be registered", name)
		}
	}
}

func TestDiscoverReadsOnlyFrontmatterUntilSkillIsInvoked(t *testing.T) {
	global := t.TempDir()
	project := t.TempDir()
	writeTestSkill(t, global, "release", `---
name: release
description: Prepare a release.
tools:
  - Git
---
old instructions
`)
	writeTestSkill(t, project, "release", `---
name: release
description: Prepare the project release.
tools:
  - Git
  - Bash
---
project instructions
`)

	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{GlobalDir: global, ProjectDir: project}); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	listed := skillByName(t, manager.List(), "release")
	if listed.Description != "Prepare the project release." {
		t.Fatalf("project skill should override global metadata, got %q", listed.Description)
	}
	if listed.Prompt != "" {
		t.Fatalf("catalog must not load skill body, got %q", listed.Prompt)
	}

	writeTestSkill(t, project, "release", `---
name: release
description: Prepare the project release.
tools:
  - Git
  - Bash
---
fresh instructions
`)

	skill, ok := manager.Get("release")
	if !ok {
		t.Fatal("expected discovered skill to be loadable")
	}
	if skill.Prompt != "fresh instructions" {
		t.Fatalf("expected invocation to read current body, got %q", skill.Prompt)
	}
}

func TestWriteManifestExportsSkillMetadataWithoutPrompts(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "deploy", `---
name: deploy
description: Deploy the current revision.
tools: [Git, Bash]
---
do not expose this prompt in the manifest
`)

	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{ProjectDir: dir}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "nested", "skills.json")
	if err := manager.WriteManifest(manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"name": "deploy"`) || !strings.Contains(text, `"description": "Deploy the current revision."`) {
		t.Fatalf("manifest does not contain discovered metadata: %s", text)
	}
	if strings.Contains(text, "do not expose this prompt") || strings.Contains(text, `"prompt"`) {
		t.Fatalf("manifest must contain metadata only: %s", text)
	}
}

func writeTestSkill(t *testing.T, root, name, contents string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillByName(t *testing.T, items []Skill, name string) Skill {
	t.Helper()
	for _, item := range items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("skill %q not found in catalog", name)
	return Skill{}
}
