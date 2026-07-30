package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/mcp"
	"code-agent/internal/rag"
	"code-agent/internal/tools"
)

type recordingRAGIngester struct {
	files    []*os.File
	names    []string
	contents []string
	legacy   []string
	result   *rag.IngestResult
	err      error
}

func (r *recordingRAGIngester) Ingest(_ context.Context, path string) (*rag.IngestResult, error) {
	r.legacy = append(r.legacy, path)
	return r.result, r.err
}

func (r *recordingRAGIngester) IngestFile(_ context.Context, file *os.File, name string) (*rag.IngestResult, error) {
	r.files = append(r.files, file)
	r.names = append(r.names, name)
	if file != nil {
		_, _ = file.Seek(0, io.SeekStart)
		content, _ := io.ReadAll(file)
		r.contents = append(r.contents, string(content))
	}
	return r.result, r.err
}

func TestIngestCommandIsListedInHelpAndBootstrap(t *testing.T) {
	app, out := newPermissionTestApp(t.TempDir(), "")
	app.mcp = mcp.NewManager()

	app.renderBootstrap()
	if !strings.Contains(out.String(), "/ingest") {
		t.Fatalf("bootstrap commands should list /ingest: %q", out.String())
	}
	out.Reset()
	if !app.handleSlashCommand(context.Background(), "/help") {
		t.Fatal("/help should be handled")
	}
	if !strings.Contains(out.String(), "/ingest <path>") {
		t.Fatalf("help should describe /ingest <path>: %q", out.String())
	}
}

func TestRAGConfigOutputOmitsInternalSecret(t *testing.T) {
	app, out := newPermissionTestApp(t.TempDir(), "")
	app.mcp = mcp.NewManager()
	app.cfg.RAGEnabled = true
	app.cfg.RAGServerURL = "http://127.0.0.1:8081"
	app.cfg.RAGInternalSecret = "must-not-be-rendered"
	app.cfg.RAGUserID = 42
	app.cfg.RAGOrgTag = "engineering"
	app.cfg.RAGIngestPublic = true

	app.handleSlashCommand(context.Background(), "/config")

	rendered := out.String()
	for _, expected := range []string{
		"rag enabled: true",
		"rag server: http://127.0.0.1:8081",
		"rag user: 42",
		"rag org: engineering",
		"rag ingest public: true",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("config should contain %q: %q", expected, rendered)
		}
	}
	if strings.Contains(rendered, app.cfg.RAGInternalSecret) {
		t.Fatalf("config must not reveal RAG internal secret: %q", rendered)
	}
}

func TestIngestCommandRejectsInvalidInputWithoutCallingIngester(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "folder")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		command    string
		configure  func(*App)
		wantOutput string
	}{
		{name: "missing argument", command: "/ingest", wantOutput: "usage: /ingest <path>"},
		{name: "nil ingester", command: "/ingest missing.pdf", configure: func(app *App) { app.ragIngester = nil }, wantOutput: "RAG ingestion is unavailable"},
		{name: "disabled ingester", command: "/ingest missing.pdf", configure: func(app *App) { app.cfg.RAGEnabled = false }, wantOutput: "RAG ingestion is unavailable"},
		{name: "missing file", command: "/ingest missing.pdf", wantOutput: "ingest failed: file does not exist"},
		{name: "directory", command: "/ingest folder", wantOutput: "ingest failed: path is not a regular file"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, out := newPermissionTestApp(root, "")
			recorder := &recordingRAGIngester{}
			app.cfg.RAGEnabled = true
			app.ragIngester = recorder
			if test.configure != nil {
				test.configure(app)
			}

			if !app.handleSlashCommand(context.Background(), test.command) {
				t.Fatal("/ingest should be handled")
			}
			if !strings.Contains(out.String(), test.wantOutput) {
				t.Fatalf("expected %q, got %q", test.wantOutput, out.String())
			}
			if len(recorder.files) != 0 {
				t.Fatalf("invalid input must not call ingester, got %d calls", len(recorder.files))
			}
		})
	}
}

func TestIngestCommandRejectsWorkspaceEscapes(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.pdf")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	relativeEscape, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{"/ingest " + outside, "/ingest " + relativeEscape} {
		app, out := newPermissionTestApp(root, "")
		recorder := &recordingRAGIngester{}
		app.cfg.RAGEnabled = true
		app.ragIngester = recorder

		app.handleSlashCommand(context.Background(), command)

		if !strings.Contains(out.String(), "ingest failed: path is outside the workspace") {
			t.Fatalf("workspace escape should be rejected, got %q", out.String())
		}
		if len(recorder.files) != 0 {
			t.Fatalf("workspace escape must not call ingester, got %d calls", len(recorder.files))
		}
	}
}

func TestIngestCommandRejectsSymlinkEscapingWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.pdf")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.pdf")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	app, out := newPermissionTestApp(root, "")
	recorder := &recordingRAGIngester{}
	app.cfg.RAGEnabled = true
	app.ragIngester = recorder

	app.handleSlashCommand(context.Background(), "/ingest linked.pdf")

	if !strings.Contains(out.String(), "ingest failed: path is outside the workspace") {
		t.Fatalf("symlink escape should be rejected, got %q", out.String())
	}
	if len(recorder.files) != 0 {
		t.Fatalf("symlink escape must not call ingester, got %d calls", len(recorder.files))
	}
}

func TestIngestCommandUsesExecutorWorkingDirectoryAndOpenFile(t *testing.T) {
	root := t.TempDir()
	workingDir := filepath.Join(root, "docs with spaces")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(workingDir, "report final.pdf")
	if err := os.WriteFile(file, []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		command string
	}{
		{name: "double quoted", command: `/ingest "report final.pdf"`},
		{name: "single quoted", command: `/ingest 'report final.pdf'`},
		{name: "unquoted with spaces", command: `/ingest report final.pdf`},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, out := newPermissionTestApp(root, "")
			app.executor = tools.NewExecutor(root)
			if err := app.executor.SetWorkingDir(workingDir); err != nil {
				t.Fatal(err)
			}
			recorder := &recordingRAGIngester{result: &rag.IngestResult{FileName: "report final.pdf", FileMD5: "abc123", Message: "queued"}}
			app.cfg.RAGEnabled = true
			app.ragIngester = recorder

			app.handleSlashCommand(context.Background(), test.command)

			if len(recorder.files) != 1 || len(recorder.names) != 1 || len(recorder.contents) != 1 {
				t.Fatalf("expected one open-file call, got files=%d names=%d contents=%d", len(recorder.files), len(recorder.names), len(recorder.contents))
			}
			if recorder.names[0] != "report final.pdf" || recorder.contents[0] != "pdf" {
				t.Fatalf("open-file call = name %q content %q", recorder.names[0], recorder.contents[0])
			}
			rendered := out.String()
			for _, expected := range []string{"report final.pdf", "abc123", "queued"} {
				if !strings.Contains(rendered, expected) {
					t.Fatalf("successful output should contain %q: %q", expected, rendered)
				}
			}
		})
	}
}

func TestIngestCommandSanitizesLocalPathResolutionErrors(t *testing.T) {
	root := t.TempDir()
	missingRoot := filepath.Join(root, "missing workspace")
	app, out := newPermissionTestApp(root, "")
	app.cfg.ProjectRoot = missingRoot
	app.cfg.RAGEnabled = true
	app.ragIngester = &recordingRAGIngester{}

	app.handleSlashCommand(context.Background(), "/ingest report.pdf")

	rendered := out.String()
	if !strings.Contains(rendered, "ingest failed: workspace is unavailable") {
		t.Fatalf("expected readable workspace error, got %q", rendered)
	}
	if strings.Contains(rendered, root) || strings.Contains(rendered, missingRoot) {
		t.Fatalf("local path resolution error must not expose absolute paths: %q", rendered)
	}
}

func TestIngestCommandReportsReadableSanitizedError(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "report.pdf")
	if err := os.WriteFile(file, []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	app, out := newPermissionTestApp(root, "")
	app.cfg.RAGEnabled = true
	rawError := `{"error":"X-Internal-Token: super-secret","path":"` + file + `","quoted":"'` + file + `'"}`
	app.ragIngester = &recordingRAGIngester{err: errors.New(rawError)}

	app.handleSlashCommand(context.Background(), "/ingest report.pdf")

	rendered := out.String()
	if rendered != "ingest failed: RAG ingestion request failed\n" {
		t.Fatalf("expected fixed failure, got %q", rendered)
	}
	if strings.Contains(rendered, "super-secret") || strings.Contains(rendered, root) || strings.Contains(rendered, "X-Internal-Token") {
		t.Fatalf("failure must not expose secrets or absolute paths: %q", rendered)
	}
}
