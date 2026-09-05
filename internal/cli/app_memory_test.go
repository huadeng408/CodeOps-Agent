package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/config"
)

func TestAppRunFailsClosedWhenMemoryInitializationFails(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "memory initialization failed") {
		t.Fatalf("Run error = %v, want fail-closed memory initialization error", err)
	}
}
