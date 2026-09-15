package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestNewManagerProvidesGoalSkillCatalog(t *testing.T) {
	manager := NewManager()
	items := manager.List()
	if len(items) < 40 {
		t.Fatalf("goal requires at least 40 runnable skills, got %d", len(items))
	}
	seen := make(map[string]struct{}, len(items))
	for _, skill := range items {
		if skill.Name == "" || skill.Description == "" || skill.Prompt == "" || len(skill.Tools) == 0 {
			t.Fatalf("skill %q is not runnable metadata: %+v", skill.Name, skill)
		}
		if _, ok := seen[skill.Name]; ok {
			t.Fatalf("duplicate skill name %q", skill.Name)
		}
		seen[skill.Name] = struct{}{}
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

func TestWriteManifestPreservesRoutingAndInvocationMetadataWithoutPrompt(t *testing.T) {
	dir := t.TempDir()
	contents := strings.Join([]string{
		"---",
		"name: model-only",
		"description: Model-only routing skill.",
		"whenToUse: Use when the model needs repository context.",
		"disable-model-invocation: false",
		"user-invocable: false",
		"---",
		"private instructions",
	}, "\n")
	writeTestSkill(t, dir, "model-only", contents)
	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{ProjectDir: dir}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	manifest := filepath.Join(t.TempDir(), "skills.json")
	if err := manager.WriteManifest(manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"model-only", "Use when the model needs repository context.", "modelInvocable", "userInvocable"} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest missing %s: %s", want, text)
		}
	}
	if strings.Contains(text, "private instructions") || strings.Contains(text, "prompt") {
		t.Fatalf("manifest must not expose prompt body: %s", text)
	}
}

func TestDiscoverRejectsInvalidNamesAndToolMetadata(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "aaa-valid", `---
name: aaa-valid
description: Valid but must not be partially committed
tools: [Git]
---
body
`)
	writeTestSkill(t, dir, "invalid-name", `---
name: Bad_Name
description: Invalid name
tools: [Git]
---
body
`)
	writeTestSkill(t, dir, "invalid-tool", "---\nname: invalid-tool\ndescription: Invalid tool\ntools: [\"Git\\nBash\"]\n---\nbody\n")

	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{ProjectDir: dir}); err == nil {
		t.Fatal("Discover should fail closed for invalid Skill metadata")
	}
	if _, ok := manager.Get("Bad_Name"); ok {
		t.Fatal("invalid Skill name must not be registered")
	}
	if _, ok := manager.Get("invalid-tool"); ok {
		t.Fatal("invalid tool metadata must not be registered")
	}
	if _, ok := manager.Get("aaa-valid"); ok {
		t.Fatal("directory discovery must not partially commit before validation completes")
	}
}

func TestDiscoverSupportsFlatFilesAndDeepSeekMetadata(t *testing.T) {
	root := t.TempDir()
	flat := filepath.Join(root, "flat.md")
	contents := `---
name: flat
description: Flat skill
whenToUse: Use for flat-file checks.
disable-model-invocation: true
user-invocable: false
---
flat body
`
	if err := os.WriteFile(flat, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{Directories: []string{root}}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	skill, ok := manager.Get("flat")
	if !ok {
		t.Fatal("expected flat skill")
	}
	if skill.WhenToUse != "Use for flat-file checks." || skill.Source == "" || skill.Provider == "" || skill.Path != flat {
		t.Fatalf("missing deepseek metadata: %+v", skill)
	}
	if skill.Invocation.ModelInvocable || skill.Invocation.UserInvocable {
		t.Fatalf("invocation policy not applied: %+v", skill.Invocation)
	}
}

func TestReadResourceConfinesAccessToSkillDirectory(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "resource-skill", "---\nname: resource-skill\ndescription: Resource skill\n---\nbody\n")
	if err := os.MkdirAll(filepath.Join(root, "resource-skill", "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "resource-skill", "resources", "guide.txt"), []byte("guide"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{ProjectDir: root}); err != nil {
		t.Fatal(err)
	}
	data, err := manager.ReadResource("resource-skill", "resources/guide.txt")
	if err != nil || string(data) != "guide" {
		t.Fatalf("read resource: %q %v", data, err)
	}
	if _, err := manager.ReadResource("resource-skill", "../SKILL.md"); err == nil {
		t.Fatal("resource traversal must fail")
	}
}

func TestSkillWindowsFrontmatterAndAllowedTools(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "portable", "\ufeff---\r\nname: portable\r\ndescription: Portable workflow\r\nallowed-tools: Read, Grep Bash\r\n---\r\nRun the workflow.\r\n---not-a-delimiter\r\n")
	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{ProjectDir: root}); err != nil {
		t.Fatal(err)
	}
	metadata := skillByName(t, manager.List(), "portable")
	if metadata.Prompt != "" || strings.Join(metadata.Tools, ",") != "Read,Grep,Bash" {
		t.Fatalf("unexpected lazy metadata: %+v", metadata)
	}
	skill, ok, err := manager.LoadForModel("portable")
	if err != nil || !ok || skill.Prompt != "Run the workflow.\r\n---not-a-delimiter" {
		t.Fatalf("LoadForModel = %+v, %v, %v", skill, ok, err)
	}
}

func TestReadResourceRejectsSymlinkEscapeAndOversizedFile(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root, "confined", "---\nname: confined\ndescription: Confined resources\n---\nbody\n")
	base := filepath.Join(root, "confined")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{ProjectDir: root}); err != nil {
		t.Fatal(err)
	}
	t.Run("symlink", func(t *testing.T) {
		if err := os.Symlink(outside, filepath.Join(base, "escape.txt")); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		if _, err := manager.ReadResource("confined", "escape.txt"); err == nil {
			t.Fatal("symlink escape must fail")
		}
	})
	t.Run("oversized", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(base, "large.txt"), []byte(strings.Repeat("x", 256*1024+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.ReadResource("confined", "large.txt"); err == nil {
			t.Fatal("oversized resource must fail")
		}
	})
}

func TestReadResourceRejectsWindowsJunctionEscape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows junction test")
	}
	root := t.TempDir()
	writeTestSkill(t, root, "confined", "---\nname: confined\ndescription: Confined resources\n---\nbody\n")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "guide.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(root, "confined", "escape")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v, %s", err, output)
	}
	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{ProjectDir: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReadResource("confined", "escape/guide.txt"); err == nil {
		t.Fatal("junction escape must fail")
	}
}

func TestDiscoverPriorityUsesProjectDSHBeforeAgentsAndCustom(t *testing.T) {
	project := t.TempDir()
	dsh := filepath.Join(project, ".dsh", "skills")
	agents := filepath.Join(project, ".agents", "skills")
	custom := t.TempDir()
	for _, item := range []struct{ root, body string }{{dsh, "dsh"}, {agents, "agents"}, {custom, "custom"}} {
		writeTestSkill(t, item.root, "same", "---\nname: same\ndescription: "+item.body+"\n---\n"+item.body)
	}
	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{ProjectDSHDir: dsh, ProjectAgentsDir: agents, Directories: []string{custom}}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	skill, ok := manager.Get("same")
	if !ok {
		t.Fatal("expected same")
	}
	if skill.Description != "dsh" {
		t.Fatalf("expected project dsh winner, got %+v", skill)
	}
}

func TestSnapshotMarksIncompleteAndRetainsLastGoodCatalog(t *testing.T) {
	manager := NewManager()
	manager.SetDiscoveryStatus(true)
	first := manager.Snapshot()
	if !first.Complete || first.Revision == 0 {
		t.Fatalf("expected initial complete revision, got %+v", first)
	}
	manager.SetDiscoveryStatus(false)
	second := manager.Snapshot()
	if second.Complete || second.Count != first.Count || second.Revision != first.Revision {
		t.Fatalf("last-good catalog lost: first=%+v second=%+v", first, second)
	}
}

func TestRegisterRejectsInvalidRegisteredMetadata(t *testing.T) {
	manager := NewManager()
	manager.Register(Skill{Name: "Bad_Name", Description: "invalid", Prompt: "body"})
	if _, ok := manager.Get("Bad_Name"); ok {
		t.Fatal("Register should reject invalid Skill names")
	}
	manager.Register(Skill{Name: "safe-skill", Description: "invalid tools", Tools: []string{"Git\nBash"}})
	if _, ok := manager.Get("safe-skill"); ok {
		t.Fatal("Register should reject invalid tool metadata")
	}
	if err := manager.WriteManifest(filepath.Join(t.TempDir(), "skills.json")); err != nil {
		t.Fatalf("built-in manifest should remain writable: %v", err)
	}
}

func TestRegisterPreservesExplicitlyDisabledInvocationPolicy(t *testing.T) {
	manager := NewManager()
	manager.Register(Skill{
		Name:        "internal-only",
		Description: "An internal-only runtime skill.",
		Prompt:      "private runtime instructions",
		Invocation:  InvocationPolicy{ModelInvocable: false, UserInvocable: false, Configured: true},
	})
	skill, ok := manager.Get("internal-only")
	if !ok {
		t.Fatal("expected runtime Skill to be registered")
	}
	if skill.Invocation.ModelInvocable || skill.Invocation.UserInvocable {
		t.Fatalf("explicitly disabled policy was overwritten: %+v", skill.Invocation)
	}
}

func TestListFiltersInvocationPolicyViews(t *testing.T) {
	manager := NewManager()
	manager.Register(Skill{
		Name:        "internal-only",
		Description: "runtime-only skill",
		Prompt:      "internal body",
		Tools:       []string{"Read"},
		Invocation:  InvocationPolicy{ModelInvocable: false, UserInvocable: false, Configured: true},
	})

	if !containsSkill(manager.List(), "internal-only") {
		t.Fatal("default List should retain the complete catalog")
	}
	if containsSkill(manager.ListForModel(), "internal-only") {
		t.Fatal("model view must exclude non-model-invocable skills")
	}
	if containsSkill(manager.ListForUser(), "internal-only") {
		t.Fatal("user view must exclude non-user-invocable skills")
	}
}

func TestLoadViewsRejectDisallowedSkillsBeforeReadingPrompt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "internal-only", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: internal-only\ndescription: private\nuser-invocable: false\ndisable-model-invocation: true\n---\nprivate body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	if err := manager.Discover(DiscoveryOptions{ProjectDir: root}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Remove the body after metadata discovery. Policy checks must happen before
	// any attempt to read the now-missing prompt.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := manager.LoadForUser("internal-only"); err == nil || ok || !strings.Contains(err.Error(), "not user-invocable") {
		t.Fatalf("LoadForUser = ok=%v err=%v, want policy denial", ok, err)
	}
	if _, ok, err := manager.LoadForModel("internal-only"); err == nil || ok || !strings.Contains(err.Error(), "not model-invocable") {
		t.Fatalf("LoadForModel = ok=%v err=%v, want policy denial", ok, err)
	}
}

func containsSkill(items []Skill, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
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
