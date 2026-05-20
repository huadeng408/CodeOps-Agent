package codeagent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/memory"
)

func TestMemoryManagerPersistsMarkdownMemories(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManager(dir)

	item := manager.Add("Prefer Markdown memory files for project facts.", "project", "#memory")
	if item.Name == "" {
		t.Fatal("memory name should be assigned")
	}
	if item.Tags[1] != "memory" {
		t.Fatalf("expected normalized tags, got %#v", item.Tags)
	}

	if _, err := os.Stat(filepath.Join(dir, item.Name+".md")); err != nil {
		t.Fatalf("memory markdown file missing: %v", err)
	}
	index, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("index file missing: %v", err)
	}
	if !strings.Contains(string(index), item.Name) {
		t.Fatalf("index missing memory name: %s", string(index))
	}

	reloaded := memory.NewManager(dir)
	matches := reloaded.LoadRelevant("markdown")
	if len(matches) != 1 {
		t.Fatalf("expected one relevant memory, got %d", len(matches))
	}
	if matches[0].Content != "Prefer Markdown memory files for project facts." {
		t.Fatalf("unexpected memory content: %q", matches[0].Content)
	}
}

func TestMemoryManagerDeletesMemories(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManager(dir)
	item := manager.Add("Temporary note to delete.")

	if err := manager.Delete(item.Name); err != nil {
		t.Fatalf("delete memory: %v", err)
	}
	if _, ok := manager.Get(item.Name); ok {
		t.Fatal("deleted memory should not be returned")
	}
	if _, err := os.Stat(filepath.Join(dir, item.Name+".md")); !os.IsNotExist(err) {
		t.Fatalf("memory file should be removed, got err=%v", err)
	}
}
