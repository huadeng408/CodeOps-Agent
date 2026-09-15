package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/config"
	"code-agent/internal/memory"
)

func TestAppDoesNotRewriteLegacyMemoryAndExplicitImportFailsClosed(t *testing.T) {
	root := t.TempDir()
	memoryDir := filepath.Join(root, "memory")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "broken.md"), []byte("not frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(root)
	cfg.ProjectRoot = root
	cfg.WorkingDir = root
	cfg.MemoryDir = memoryDir
	cfg.OrchestratorAutoStart = false

	app := NewApp(cfg, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	err := app.Run(context.Background())
	if err != nil {
		t.Fatalf("Ledger CLI was blocked by unused legacy memory: %v", err)
	}
	adapter := app.memory.(*memory.CLIAdapter)
	if _, err := adapter.ImportLegacy(memoryDir); err == nil {
		t.Fatal("corrupt legacy memory imported")
	}
	data, err := os.ReadFile(filepath.Join(memoryDir, "broken.md"))
	if err != nil || string(data) != "not frontmatter\n" {
		t.Fatal("legacy file changed")
	}
}
