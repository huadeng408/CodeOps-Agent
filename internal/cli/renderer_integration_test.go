package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRendererPipeTranscriptIsLinearAndSafe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, reader)
		close(done)
	}()

	r := NewStreamRendererWithCapabilities(writer, TerminalCapabilities{Width: 80})
	r.PrintBootstrap(BootstrapView{Version: "v0.1", Branch: "main", Workspace: `D:\repo`, Mode: "chat", Model: "deepseek-v4-pro"})
	r.ToolStarted("Test", "internal/rag")
	r.ToolCompleted(ToolEvent{Name: "Test", Target: "internal/rag", Summary: "18 passed", Duration: time.Second})
	r.PrintPermission(PermissionView{Tool: "Shell", Reason: "changes repository state", Parameters: "git status"})
	r.PrintInterrupted()
	_ = writer.Close()
	<-done

	got := output.String()
	if strings.Contains(got, "\x1b[") || strings.ContainsAny(got, "╭╮╰╯鈺鈹") {
		t.Fatalf("pipe transcript contains control or corrupted panel bytes: %q", got)
	}
	for _, want := range []string{"code-agent v0.1", "[ok] Test internal/rag | 18 passed | 1.0s", "Permission required", "[interrupted]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("pipe transcript missing %q: %q", want, got)
		}
	}
}

func TestRendererNarrowTranscriptStaysWithinConfiguredWidth(t *testing.T) {
	const width = 40
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: width})
	r.PrintBootstrap(BootstrapView{Version: "v0.1", Branch: "main", Workspace: `D:\vscode\localcode`, Mode: "chat", Model: "deepseek-v4-pro"})
	r.ToolStartedWithID("test-1", "Test", "internal/rag")
	r.ToolCompleted(ToolEvent{ToolCallID: "test-1", Name: "Test", Target: "internal/rag", Summary: "18 passed", Duration: 1400 * time.Millisecond})
	r.ToolCompleted(ToolEvent{Name: "Test", Target: "internal/rag", ExitCode: 1, Duration: 2100 * time.Millisecond, Detail: "FAIL TestSpan\nexpected closed, got active", Truncated: true})
	r.PrintPermission(PermissionView{Tool: "Shell", Reason: "changes repository state", Parameters: `git commit -m "fix: close rag spans"`})
	r.PrintQuestion(QuestionView{Question: "Choose an index", Options: []QuestionOption{{Label: "hybrid", Description: "dense + lexical"}, {Label: "visual", Description: "image-aware"}}})
	r.PrintDiff(DiffSummary{Files: 4, Added: 82, Removed: 17, Lines: []string{"M internal/cli/renderer.go", "A internal/cli/theme.go"}, Truncated: true})
	r.PrintStatus("chat | errors 0 | tokens 1200 in / 300 out | cost $0.0120")

	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if got := cellWidth(line); got > width {
			t.Fatalf("narrow preview line width %d exceeds %d: %q", got, width, line)
		}
	}
}
