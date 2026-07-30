//go:build windows

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/rag"
)

func TestIngestCommandRejectsDirectoryJunctionEscapingWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.pdf"), []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(root, "outside-link")
	createJunction(t, junction, outside)

	app, out := newPermissionTestApp(root, "")
	recorder := &recordingRAGIngester{}
	app.cfg.RAGEnabled = true
	app.ragIngester = recorder
	app.handleSlashCommand(context.Background(), `/ingest outside-link\secret.pdf`)

	if !strings.Contains(out.String(), "ingest failed: path is outside the workspace") {
		t.Fatalf("junction escape should be rejected, got %q", out.String())
	}
	if len(recorder.files) != 0 || len(recorder.legacy) != 0 {
		t.Fatalf("junction escape must not call ingester, got open=%d legacy=%d", len(recorder.files), len(recorder.legacy))
	}
}

func TestIngestCommandAcceptsWorkspaceRootJunction(t *testing.T) {
	realRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(realRoot, "inside.pdf"), []byte("inside knowledge"), 0o600); err != nil {
		t.Fatal(err)
	}
	junctionRoot := filepath.Join(t.TempDir(), "workspace-link")
	createJunction(t, junctionRoot, realRoot)

	app, out := newPermissionTestApp(junctionRoot, "")
	app.executor = nil
	app.cfg.RAGEnabled = true
	recorder := &recordingRAGIngester{result: &rag.IngestResult{FileName: "inside.pdf", FileMD5: "abc", Message: "queued"}}
	app.ragIngester = recorder
	app.handleSlashCommand(context.Background(), "/ingest inside.pdf")

	if len(recorder.files) != 1 || recorder.names[0] != "inside.pdf" || recorder.contents[0] != "inside knowledge" {
		t.Fatalf("junction workspace should ingest opened file, output=%q open=%d legacy=%d", out.String(), len(recorder.files), len(recorder.legacy))
	}
}

func createJunction(t *testing.T, link, target string) {
	t.Helper()
	output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("create directory junction %q -> %q: %v: %s", link, target, err, strings.TrimSpace(string(output)))
	}
}
