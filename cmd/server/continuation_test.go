package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/tools"
)

func TestConfigureContinuationSkillsLoadsProjectSkill(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".agent", "skills", "repo-explorer")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: repo-explorer\ndescription: Explore the repository.\ntools: [Read, Glob, Grep]\n---\ninspect the repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	executor := tools.NewExecutor(root)
	defer executor.Close()
	if err := configureContinuationSkills(executor, root); err != nil {
		t.Fatalf("skill discovery: %v", err)
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Skill", Arguments: map[string]any{"name": "repo-explorer"},
	})
	if err != nil {
		t.Fatalf("execute Skill: %v", err)
	}
	if result.Error != "" || !strings.Contains(result.Output, "inspect the repository") {
		t.Fatalf("unexpected Skill result: %+v", result)
	}
}

func TestConfigureContinuationSkillsUsesDeepSeekProjectPrecedence(t *testing.T) {
	root := t.TempDir()
	for _, item := range []struct {
		dir  string
		body string
	}{
		{filepath.Join(root, ".agents", "skills"), "agents body"},
		{filepath.Join(root, ".dsh", "skills"), "dsh body"},
		{filepath.Join(root, ".agent", "skills"), "legacy body"},
	} {
		dir := filepath.Join(item.dir, "same")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		contents := "---\nname: same\ndescription: " + item.body + "\ntools: [Read]\n---\n" + item.body + "\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	executor := tools.NewExecutor(root)
	defer executor.Close()
	if err := configureContinuationSkills(executor, root); err != nil {
		t.Fatalf("skill discovery: %v", err)
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Skill", Arguments: map[string]any{"name": "same"},
	})
	if err != nil {
		t.Fatalf("execute Skill: %v", err)
	}
	if result.Error != "" || !strings.Contains(result.Output, "dsh body") {
		t.Fatalf("project .dsh Skill should win, got %+v", result)
	}
}

func TestContinuationToolExecutorsIsolateConcurrentWorkingDirs(t *testing.T) {
	root := t.TempDir()
	left := filepath.Join(root, "left")
	right := filepath.Join(root, "right")
	for dir, marker := range map[string]string{left: "LEFT_MARKER", right: "RIGHT_MARKER"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(marker), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager := newContinuationToolExecutors(root)
	defer manager.Close()

	type result struct {
		output string
		err    string
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, tc := range []struct {
		sessionID  string
		workingDir string
		marker     string
	}{
		{sessionID: "session-left", workingDir: left, marker: "LEFT_MARKER"},
		{sessionID: "session-right", workingDir: right, marker: "RIGHT_MARKER"},
	} {
		tc := tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := manager.ExecuteInWorkingDir(context.Background(), identity.Actor{}, tc.sessionID, tc.workingDir, orchestrator.ToolCall{
				ID: tc.sessionID, Name: "Read", ParametersJSON: `{"path":"README.md"}`,
			})
			results <- result{output: got.Output, err: got.Error}
			if !strings.Contains(got.Output, tc.marker) {
				t.Errorf("session %s read %q, want %s", tc.sessionID, got.Output, tc.marker)
			}
		}()
	}
	wg.Wait()
	close(results)
	for got := range results {
		if got.err != "" {
			t.Fatalf("isolated read failed: %s", got.err)
		}
	}
}

func TestConfigureContinuationSkillsKeepsBuiltinsWhenOptionalDirectoryMissing(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	defer executor.Close()
	if err := configureContinuationSkills(executor, root); err != nil {
		t.Fatalf("missing optional directory should not fail discovery: %v", err)
	}
	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Skill", Arguments: map[string]any{"name": "commit"},
	})
	if err != nil || result.Error != "" || result.Output == "" {
		t.Fatalf("built-in Skill should remain available: err=%v result=%+v", err, result)
	}
}
