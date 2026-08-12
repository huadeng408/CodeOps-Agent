package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStreamRendererPrintsCompactBootstrap(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 80})
	r.PrintBootstrap(BootstrapView{Version: "v0.1", Branch: "main", Workspace: `D:\repo`, Mode: "chat", Model: "deepseek-v4-pro"})

	got := out.String()
	for _, want := range []string{"code-agent v0.1", `main | D:\repo`, "chat | deepseek-v4-pro | /help"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bootstrap missing %q: %q", want, got)
		}
	}
	if strings.ContainsAny(got, "╭╮╰╯") {
		t.Fatalf("bootstrap must not use a panel: %q", got)
	}
}

func TestStreamRendererNonTTYToolLifecycleIsAuditLog(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 80})
	r.ToolStarted("Test", "internal/rag")
	r.ToolCompleted(ToolEvent{Name: "Test", Target: "internal/rag", Summary: "18 passed", Duration: 1400 * time.Millisecond})

	got := out.String()
	if !strings.Contains(got, "* Test internal/rag | running\n") || !strings.Contains(got, "[ok] Test internal/rag | 18 passed | 1.4s\n") {
		t.Fatalf("unexpected lifecycle: %q", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("plain output contains cursor control: %q", got)
	}
}

func TestStreamRendererInteractiveSuccessReplacesActiveRow(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Interactive: true, Unicode: true, Width: 80})
	r.ToolStarted("Read", "README.md")
	r.ToolCompleted(ToolEvent{Name: "Read", Target: "README.md", Summary: "42 lines"})

	got := out.String()
	if strings.Count(got, "\r\x1b[2K") != 2 || !strings.HasSuffix(got, "[ok] Read README.md | 42 lines\n") {
		t.Fatalf("active row was not replaced safely: %q", got)
	}
}

func TestStreamRendererCommitsActiveRowBeforeAssistant(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Interactive: true, Width: 80})
	r.ToolStarted("Search", "docs")
	r.AppendAssistantText("Found it")

	got := out.String()
	if !strings.Contains(got, "* Search docs | running\nassistant > Found it") {
		t.Fatalf("assistant corrupted active row: %q", got)
	}
}

func TestStreamRendererExpandsFailure(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 44})
	r.ToolCompleted(ToolEvent{Name: "Test", Target: "internal/rag", ExitCode: 1, Duration: 2100 * time.Millisecond, Detail: "FAIL TestSpan\nexpected closed, got active", Truncated: true})

	got := out.String()
	for _, want := range []string{"[fail] Test internal/rag | exit 1 | 2.1s", "  FAIL TestSpan", "  expected closed, got active", "  [output truncated]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("failure missing %q: %q", want, got)
		}
	}
}

func TestStreamRendererPrintsPermissionQuestionAndDiff(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 80})
	r.PrintPermission(PermissionView{Tool: "Shell", Reason: "changes repository state", Parameters: "git commit", AllowSession: true})
	r.PrintQuestion(QuestionView{Question: "Choose a path", Options: []QuestionOption{{Label: "alpha", Description: "first"}}})
	r.PrintDiff(DiffSummary{Files: 4, Added: 82, Removed: 17, Lines: []string{"M internal/cli/renderer.go"}, Truncated: true})

	got := out.String()
	for _, want := range []string{"Permission required", "[1] Allow once", "[2] Allow session", "[Esc] Deny", "Question", "[1] alpha", "Changes | 4 files | +82 -17", "[diff truncated]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("semantic output missing %q: %q", want, got)
		}
	}
}

func TestStreamRendererASCIIAndInterruptAreDurable(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 80})
	r.PrintInterrupted()
	r.PrintError("connection lost")
	got := out.String()
	if got != "[interrupted] Ready for new instructions.\n[error] connection lost\n" {
		t.Fatalf("unexpected durable output: %q", got)
	}
}
