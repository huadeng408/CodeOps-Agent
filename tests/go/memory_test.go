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

	item, err := manager.Add("Prefer Markdown memory files for project facts.", "project", "#memory")
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
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

func TestMemoryManagerRenamingRemovesStaleFileAfterRestart(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManager(dir)

	saved, err := manager.Save(memory.Memory{ID: "memory-1", Name: "original-note", Content: "Keep one durable note."})
	if err != nil {
		t.Fatalf("save original memory: %v", err)
	}
	if _, err := manager.Save(memory.Memory{ID: saved.ID, Name: "renamed-note", Content: saved.Content, CreatedAt: saved.CreatedAt}); err != nil {
		t.Fatalf("save renamed memory: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "original-note.md")); !os.IsNotExist(err) {
		t.Fatalf("stale memory file should be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "renamed-note.md")); err != nil {
		t.Fatalf("renamed memory file missing: %v", err)
	}
	if got := manager.List(); len(got) != 1 || got[0].Name != "renamed-note" {
		t.Fatalf("expected one renamed memory, got %+v", got)
	}

	reloaded := memory.NewManager(dir)
	if got := reloaded.List(); len(got) != 1 || got[0].Name != "renamed-note" {
		t.Fatalf("expected one renamed memory after restart, got %+v", got)
	}
}

func TestMemoryManagerDeletesMemories(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManager(dir)
	item, err := manager.Add("Temporary note to delete.")
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}

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

func TestMemoryManagerRejectsSensitiveFieldsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManager(dir)
	secretAssignment := "OPENAI_" + "API_KEY=fixture-secret"

	if _, err := manager.Add("Do not persist "+secretAssignment, "project"); err == nil {
		t.Fatal("sensitive memory content should be rejected")
	}
	if _, err := manager.Add("A safe note", "api_"+"key"); err == nil {
		t.Fatal("sensitive memory tag should be rejected")
	}
	if _, err := manager.Save(memory.Memory{Name: "api_" + "key", Content: "A safe note"}); err == nil {
		t.Fatal("sensitive memory name should be rejected")
	}

	entries, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Base(entries[0]) != "MEMORY.md" {
		t.Fatalf("unexpected memory files after rejection: %v", entries)
	}
}

func TestMemoryManagerDoesNotLoadSensitiveMarkdown(t *testing.T) {
	dir := t.TempDir()
	secretAssignment := "OPENAI_" + "API_KEY=fixture-secret"
	content := "---\n" +
		"id: leaked\n" +
		"name: safe-name\n" +
		"tags: project\n" +
		"created_at: 2026-09-05T00:00:00Z\n" +
		"updated_at: 2026-09-05T00:00:00Z\n" +
		"---\n" + secretAssignment + "\n"
	if err := os.WriteFile(filepath.Join(dir, "leaked.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := memory.NewManager(dir)
	if got := manager.List(); len(got) != 0 {
		t.Fatalf("sensitive markdown should not be loaded: %+v", got)
	}
}

func TestMemoryManagerReportsLoadErrorsWithoutOverwritingIndex(t *testing.T) {
	dir := t.TempDir()
	index := []byte("# existing index\n")
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), index, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("not frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := memory.NewManager(dir)
	if manager.Err() == nil {
		t.Fatal("memory load error should be observable")
	}
	got, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(index) {
		t.Fatalf("index was overwritten after load failure: %q", got)
	}
}

func TestMemoryManagerRejectsDsnCredentials(t *testing.T) {
	manager := memory.NewManager(t.TempDir())
	if _, err := manager.Add("Do not persist postgres://user:password@db.example/app"); err == nil {
		t.Fatal("DSN credentials should be rejected")
	}
}

func TestMemoryManagerRejectsSensitiveFilenameFallback(t *testing.T) {
	dir := t.TempDir()
	content := "---\n" +
		"id: memory-1\n" +
		"tags: project\n" +
		"created_at: 2026-09-05T00:00:00Z\n" +
		"updated_at: 2026-09-05T00:00:00Z\n" +
		"---\n" +
		"ordinary implementation note\n"
	if err := os.WriteFile(filepath.Join(dir, "api-key.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := memory.NewManager(dir)
	if manager.Err() == nil {
		t.Fatal("sensitive filename fallback should fail closed")
	}
}

func TestMemoryManagerAllowsOrdinaryCredentialDiscussionNames(t *testing.T) {
	manager := memory.NewManager(t.TempDir())
	for _, name := range []string{"password-rotation-discussion", "secret-design-notes"} {
		if _, err := manager.Save(memory.Memory{Name: name, Content: "Document the implementation tradeoffs."}); err != nil {
			t.Fatalf("ordinary discussion name %q was rejected: %v", name, err)
		}
	}
}
