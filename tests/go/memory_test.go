package codeagent_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/memory"
)

func TestMemoryLegacyDefaultsAreAppliedWithoutRewritingSource(t *testing.T) {
	dir := t.TempDir()
	legacy := "---\n" +
		"id: legacy-1\n" +
		"name: legacy-note\n" +
		"tags: project\n" +
		"created_at: 2026-09-05T00:00:00Z\n" +
		"updated_at: 2026-09-05T00:00:00Z\n" +
		"---\n" +
		"legacy body\n"
	path := filepath.Join(dir, "legacy-note.md")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := memory.NewManager(dir)
	item, ok := manager.Get("legacy-note")
	if !ok {
		t.Fatal("legacy memory was not loaded")
	}
	if item.Namespace != "user" || item.Kind != "default" || item.Detail != "full" {
		t.Fatalf("legacy defaults missing: %+v", item)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != legacy {
		t.Fatal("loading legacy memory must not rewrite its source file")
	}
}

func TestMemoryMetadataRoundTripsWithProvenanceChecksum(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManager(dir)
	sourceChecksum := fmt.Sprintf("%x", sha256.Sum256([]byte("source evidence")))

	saved, err := manager.Save(memory.Memory{
		ID:             "memory-1",
		Name:           "workflow-recovery",
		Content:        "Workers resume from verified checkpoints.",
		Tags:           []string{"workflow"},
		Namespace:      "project",
		Kind:           "experiences",
		Detail:         "overview",
		SourceURI:      "session://session-1/events/42",
		SourceChecksum: sourceChecksum,
		SessionID:      "session-1",
	})
	if err != nil {
		t.Fatalf("save metadata memory: %v", err)
	}
	if saved.Checksum == "" {
		t.Fatal("memory checksum must be assigned")
	}

	restarted := memory.NewManager(dir)
	loaded, ok := restarted.Get(saved.ID)
	if !ok {
		t.Fatal("saved memory was not reloaded")
	}
	if loaded.Namespace != "project" || loaded.Kind != "experiences" || loaded.Detail != "overview" {
		t.Fatalf("metadata was not preserved: %+v", loaded)
	}
	if loaded.SourceURI != "session://session-1/events/42" || loaded.SourceChecksum != sourceChecksum || loaded.SessionID != "session-1" {
		t.Fatalf("provenance was not preserved: %+v", loaded)
	}
	if loaded.Checksum != saved.Checksum {
		t.Fatalf("checksum changed across restart: saved=%s loaded=%s", saved.Checksum, loaded.Checksum)
	}
}

func TestMemoryRecallFiltersRanksAndLimitsDeterministically(t *testing.T) {
	manager := memory.NewManager(t.TempDir())
	for _, item := range []memory.Memory{
		{Name: "secondary", Content: "Workflow recovery remains available.", Namespace: "project", Kind: "experiences", Detail: "overview"},
		{Name: "workflow-recovery", Content: "Resume workers from checkpoints.", Tags: []string{"workflow", "recovery"}, Namespace: "project", Kind: "experiences", Detail: "overview"},
		{Name: "user-workflow", Content: "Workflow recovery for another scope.", Namespace: "user", Kind: "experiences", Detail: "overview"},
	} {
		if _, err := manager.Save(item); err != nil {
			t.Fatalf("save recall fixture: %v", err)
		}
	}

	result, err := manager.Recall("workflow recovery", memory.RecallOptions{
		Namespace: "project",
		Kind:      "experiences",
		Detail:    "overview",
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Memory.Name != "workflow-recovery" {
		t.Fatalf("unexpected ranked recall: %+v", result)
	}
	if result.Stats.Candidates != 2 || result.Stats.Returned != 1 || result.Stats.Dropped != 1 {
		t.Fatalf("unexpected recall stats: %+v", result.Stats)
	}
}

func TestMemoryRecallHonorsWholeEntryTokenBudget(t *testing.T) {
	manager := memory.NewManager(t.TempDir())
	if _, err := manager.Save(memory.Memory{Name: "workflow", Content: "workflow", Tags: []string{"workflow"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Save(memory.Memory{Name: "large", Content: "workflow " + strings.Repeat("large evidence ", 80)}); err != nil {
		t.Fatal(err)
	}

	result, err := manager.Recall("workflow", memory.RecallOptions{MaxTokens: 20})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Memory.Name != "workflow" {
		t.Fatalf("budget should retain only the whole short entry: %+v", result)
	}
	if result.Stats.UsedTokens > 20 || result.Stats.Dropped != 1 {
		t.Fatalf("budget stats are invalid: %+v", result.Stats)
	}
}

func TestMemoryRecallRejectsNegativeBounds(t *testing.T) {
	manager := memory.NewManager(t.TempDir())
	if _, err := manager.Recall("query", memory.RecallOptions{Limit: -1}); err == nil {
		t.Fatal("negative recall limit must fail")
	}
	if _, err := manager.Recall("query", memory.RecallOptions{MaxTokens: -1}); err == nil {
		t.Fatal("negative recall token budget must fail")
	}
}

func TestMemoryAuditSinkReceivesSanitizedLifecycleEvents(t *testing.T) {
	var events []memory.MemoryEvent
	manager := memory.NewManagerWithAudit(t.TempDir(), func(event memory.MemoryEvent) error {
		events = append(events, event)
		return nil
	})
	saved, err := manager.Add("derived workflow evidence", "workflow-private")
	if err != nil {
		t.Fatalf("add audited memory: %v", err)
	}
	if err := manager.Delete(saved.ID); err != nil {
		t.Fatalf("delete audited memory: %v", err)
	}
	if len(events) != 2 || events[0].Action != "save" || events[1].Action != "delete" {
		t.Fatalf("unexpected memory events: %+v", events)
	}
	if events[0].MemoryChecksum != saved.Checksum || events[0].MemoryID != saved.ID {
		t.Fatalf("event provenance mismatch: %+v", events[0])
	}
	payload, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "derived workflow evidence") || strings.Contains(string(payload), "workflow-private") {
		t.Fatalf("audit event leaked memory body or tags: %s", payload)
	}
}

func TestMemoryAuditSinkFailurePreventsWrite(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManagerWithAudit(dir, func(memory.MemoryEvent) error {
		return errors.New("ledger unavailable")
	})
	if _, err := manager.Add("safe derived knowledge"); err == nil || !strings.Contains(err.Error(), "ledger unavailable") {
		t.Fatalf("expected ledger failure, got %v", err)
	}
	if got := manager.List(); len(got) != 0 {
		t.Fatalf("failed audited write mutated memory list: %+v", got)
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Base(entries[0]) != "MEMORY.md" {
		t.Fatalf("failed audited write left files: %v", entries)
	}
}

func TestMemorySaveRollsBackWhenIndexWriteFails(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManager(dir)
	baseline, err := manager.Save(memory.Memory{
		ID: "baseline-1", Name: "baseline", Content: "keep this memory",
	})
	if err != nil {
		t.Fatalf("save baseline: %v", err)
	}

	indexPath := filepath.Join(dir, "MEMORY.md")
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("remove index fixture: %v", err)
	}
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatalf("create index conflict fixture: %v", err)
	}

	if _, err := manager.Save(memory.Memory{
		ID: "index-failure-1", Name: "index-failure", Content: "must not persist",
	}); err == nil {
		t.Fatal("save should fail when index path is a directory")
	}

	got := manager.List()
	if len(got) != 1 || got[0].ID != baseline.ID {
		t.Fatalf("failed save mutated memory list: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "index-failure.md")); !os.IsNotExist(err) {
		t.Fatalf("failed save left target file, err=%v", err)
	}
	info, err := os.Stat(indexPath)
	if err != nil || !info.IsDir() {
		t.Fatalf("failed save should preserve index conflict: info=%v err=%v", info, err)
	}
}

func TestMemorySaveRestoresExistingTargetWhenStaleRemovalFails(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManager(dir)
	original, err := manager.Save(memory.Memory{
		ID: "rename-1", Name: "original-note", Content: "original body",
	})
	if err != nil {
		t.Fatalf("save original: %v", err)
	}
	if _, err := manager.Save(memory.Memory{
		ID: "target-1", Name: "renamed-note", Content: "unrelated target",
	}); err != nil {
		t.Fatalf("save existing target: %v", err)
	}
	targetPath := filepath.Join(dir, "renamed-note.md")
	targetBefore, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read existing target: %v", err)
	}
	stalePath := filepath.Join(dir, "original-note.md")
	if err := os.Remove(stalePath); err != nil {
		t.Fatalf("remove stale file fixture: %v", err)
	}
	if err := os.Mkdir(stalePath, 0o755); err != nil {
		t.Fatalf("create stale removal conflict: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stalePath, "keep"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("populate stale removal conflict: %v", err)
	}

	if _, err := manager.Save(memory.Memory{
		ID: original.ID, Name: "renamed-note", Content: "replacement body",
	}); err == nil {
		t.Fatal("rename should fail when stale path cannot be removed")
	}

	targetAfter, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("existing target was lost after failed rename: %v", err)
	}
	if string(targetAfter) != string(targetBefore) {
		t.Fatal("failed rename did not restore existing target")
	}
	got := manager.List()
	if len(got) != 2 || got[0].ID == original.ID && got[1].ID == original.ID {
		t.Fatalf("failed rename mutated memory list: %+v", got)
	}
	info, err := os.Stat(stalePath)
	if err != nil || !info.IsDir() {
		t.Fatalf("failed rename should preserve stale conflict: info=%v err=%v", info, err)
	}
}

func TestMemoryDeleteRollsBackWhenIndexWriteFails(t *testing.T) {
	dir := t.TempDir()
	manager := memory.NewManager(dir)
	item, err := manager.Save(memory.Memory{
		ID: "delete-1", Name: "delete-me", Content: "must remain",
	})
	if err != nil {
		t.Fatalf("save delete fixture: %v", err)
	}
	path := filepath.Join(dir, item.Name+".md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read delete fixture: %v", err)
	}
	indexPath := filepath.Join(dir, "MEMORY.md")
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("remove index fixture: %v", err)
	}
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatalf("create index conflict fixture: %v", err)
	}

	if err := manager.Delete(item.ID); err == nil {
		t.Fatal("delete should fail when index path is a directory")
	}
	got, ok := manager.Get(item.ID)
	if !ok || got.ID != item.ID {
		t.Fatalf("failed delete mutated memory list: got=%+v ok=%v", got, ok)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("failed delete did not restore memory file: err=%v", err)
	}
}

func TestMemoryAuditSinkFailureRollsBackUpdateAndDelete(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	manager := memory.NewManagerWithAudit(dir, func(memory.MemoryEvent) error {
		calls++
		if calls > 1 {
			return errors.New("ledger unavailable")
		}
		return nil
	})
	item, err := manager.Save(memory.Memory{
		ID: "audit-1", Name: "audit-note", Content: "original body",
	})
	if err != nil {
		t.Fatalf("save audited fixture: %v", err)
	}
	path := filepath.Join(dir, item.Name+".md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audited fixture: %v", err)
	}

	if _, err := manager.Save(memory.Memory{
		ID: item.ID, Name: item.Name, Content: "updated body", CreatedAt: item.CreatedAt,
	}); err == nil || !strings.Contains(err.Error(), "ledger unavailable") {
		t.Fatalf("expected update audit failure, got %v", err)
	}
	afterUpdate, err := os.ReadFile(path)
	if err != nil || string(afterUpdate) != string(before) {
		t.Fatalf("failed update changed memory file: err=%v", err)
	}

	if err := manager.Delete(item.ID); err == nil || !strings.Contains(err.Error(), "ledger unavailable") {
		t.Fatalf("expected delete audit failure, got %v", err)
	}
	if _, ok := manager.Get(item.ID); !ok {
		t.Fatal("failed delete removed memory from manager")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed delete removed memory file: %v", err)
	}
}

func TestMemoryChecksumMatchesPythonCanonicalFixture(t *testing.T) {
	dir := t.TempDir()
	checksum := "f58688e6d016c2059e3868c9fedbbbeed2d7c7386099b524b94bb3f48705eb09"
	sourceChecksum := fmt.Sprintf("%x", sha256.Sum256([]byte("source")))
	fixture := "---\n" +
		"id: cross-runtime-1\n" +
		"name: unicode-fixture\n" +
		"tags: \n" +
		"namespace: project\n" +
		"kind: experiences\n" +
		"detail: full\n" +
		"source_uri: session://s1/é\n" +
		"source_checksum: " + sourceChecksum + "\n" +
		"session_id: s1\n" +
		"checksum: " + checksum + "\n" +
		"created_at: 2026-09-14T01:02:03.123456Z\n" +
		"updated_at: 2026-09-14T01:02:03.987654Z\n" +
		"---\n" +
		"Café <tag> & 你好\n"
	if err := os.WriteFile(filepath.Join(dir, "unicode-fixture.md"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := memory.NewManager(dir)
	if err := manager.Err(); err != nil {
		t.Fatalf("cross-runtime fixture load failed: %v", err)
	}
	item, ok := manager.Get("unicode-fixture")
	if !ok {
		t.Fatal("cross-runtime fixture was not loaded")
	}
	if item.Checksum != checksum {
		t.Fatalf("checksum mismatch: got=%s want=%s", item.Checksum, checksum)
	}
}

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
