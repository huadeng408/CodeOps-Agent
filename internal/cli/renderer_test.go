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
	r.ToolStartedWithID("read-1", "Read", "README.md")
	r.ToolCompleted(ToolEvent{ToolCallID: "read-1", Name: "Read", Target: "README.md", Summary: "42 lines"})

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

func TestStreamRendererErrorExpandsEvenWithZeroExitCode(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 44})
	r.ToolCompleted(ToolEvent{Name: "Test", Target: "internal/rag", Error: "runner reported a failure", ExitCode: 0, Detail: "diagnostic detail"})

	got := out.String()
	if !strings.Contains(got, "[fail] Test internal/rag") || !strings.Contains(got, "runner reported a failure") || !strings.Contains(got, "diagnostic detail") {
		t.Fatalf("error-bearing zero-exit tool must expand as failure: %q", got)
	}
	if strings.Contains(got, "exit 0") || strings.Count(got, "runner reported a failure") != 1 {
		t.Fatalf("zero exit or duplicate error must not be rendered: %q", got)
	}
}

func TestStreamRendererMatchesInteractiveRowsByToolCallID(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Interactive: true, Width: 80})
	r.ToolStartedWithID("call-a", "Read", "a.txt")
	r.ToolStartedWithID("call-b", "Read", "b.txt")
	r.ToolCompleted(ToolEvent{ToolCallID: "call-b", Name: "Read", Target: "b.txt", Summary: "B done"})
	r.ToolCompleted(ToolEvent{ToolCallID: "call-a", Name: "Read", Target: "a.txt", Summary: "A done"})

	got := out.String()
	if !strings.Contains(got, "* Read a.txt | running\n") || !strings.Contains(got, "* Read b.txt | running\n") {
		t.Fatalf("parallel starts must remain durable: %q", got)
	}
	if strings.Index(got, "[ok] Read b.txt | B done") > strings.Index(got, "[ok] Read a.txt | A done") {
		t.Fatalf("completion order must be preserved without replacing the wrong row: %q", got)
	}
}

func TestStreamRendererPrintsPermissionQuestionAndDiff(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 80})
	r.PrintPermission(PermissionView{Tool: "Shell", Reason: "changes repository state", Parameters: "git commit", AllowSession: true})
	r.PrintQuestion(QuestionView{Question: "Choose a path", Options: []QuestionOption{{Label: "alpha", Description: "first"}}})
	r.PrintDiff(DiffSummary{Files: 4, Added: 82, Removed: 17, Lines: []string{"M internal/cli/renderer.go"}, Truncated: true})

	got := out.String()
	for _, want := range []string{"Permission required", "[y] Allow session", "[n] Deny", "Question", "[1] alpha", "Changes | 4 files | +82 -17", "[diff truncated]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("semantic output missing %q: %q", want, got)
		}
	}
}

func TestStreamRendererPermissionPromptHonorsSessionAllowance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		view    PermissionView
		want    string
		notWant string
	}{
		{name: "session", view: PermissionView{Tool: "Write", Reason: "requires approval", AllowSession: true}, want: "[y] Allow session  [n] Deny", notWant: "[y] Allow once"},
		{name: "always ask", view: PermissionView{Tool: "Bash", Reason: "requires approval", AllowSession: false}, want: "[y] Allow once  [n] Deny", notWant: "Allow session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 80})
			r.PrintPermission(tc.view)
			got := out.String()
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.notWant) {
				t.Fatalf("permission prompt mismatch: got %q, want %q and not %q", got, tc.want, tc.notWant)
			}
		})
	}
}

func TestStreamRendererNarrowToolMetadataDoesNotSplitDurationToken(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 24})
	r.ToolCompleted(ToolEvent{Name: "Test", Target: "internal/rag", Summary: "18 passed", Duration: 1400 * time.Millisecond})
	got := out.String()
	if strings.Contains(got, "| 1\n.4s") || strings.Contains(got, "| 1.4\ns") {
		t.Fatalf("duration token was split: %q", got)
	}
	if !strings.Contains(got, "[ok] Test internal/rag") || !strings.Contains(got, "1.4s") {
		t.Fatalf("tool metadata should retain complete duration token: %q", got)
	}
}

func TestStreamRendererNarrowStatusDoesNotSplitToken(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 24})
	r.PrintStatus("chat | errors 0 | tokens 1200 in / 300 out | cost $0.0120")
	got := out.String()
	if strings.Contains(got, "1\n200") || strings.Contains(got, "300\nout") || strings.Contains(got, "0.\n0120") {
		t.Fatalf("status token was split: %q", got)
	}
	if !strings.Contains(got, "tokens 1200 in / 300 out") {
		t.Fatalf("status should retain its complete token field: %q", got)
	}
}

func TestStreamRendererStatusStaysSingleLineWhenItFits(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 120})
	r.PrintStatus("chat | errors 0 | tokens 1200 in / 300 out | cost $0.0120")
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], " | ") {
		t.Fatalf("status should remain one joined line when it fits: %q", out.String())
	}
}

func TestStreamRendererEmptyToolCallIDNeverReplacesActiveRow(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Interactive: true, Width: 80})
	r.ToolStartedWithID("", "Read", "a.txt")
	r.ToolCompleted(ToolEvent{ToolCallID: "", Name: "Write", Target: "b.txt", Summary: "done"})
	got := out.String()
	if strings.Contains(got, "\x1b[2K") || !strings.Contains(got, "* Read a.txt | running") || !strings.Contains(got, "[ok] Write b.txt | done") {
		t.Fatalf("empty IDs must be durable and never replace active rows: %q", got)
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

func TestStreamRendererAllSemanticOutputHonorsNarrowWidth(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 16})
	r.PrintBootstrap(BootstrapView{Version: "v0.1", Branch: "feature/long-branch", Workspace: `D:\very\long\workspace`, Mode: "chat", Model: "deepseek-v4-pro"})
	r.ToolStartedWithID("tool-1", "Search", "文档目录/long-target")
	r.ToolCompleted(ToolEvent{ToolCallID: "tool-1", Name: "Search", Target: "文档目录/long-target", Summary: "completed successfully"})
	r.PrintPermission(PermissionView{Tool: "Shell", Reason: "changes repository state", Parameters: "git commit --message long-message"})
	r.PrintQuestion(QuestionView{Question: "Choose a very long retrieval strategy", Options: []QuestionOption{{Label: "hybrid-retrieval", Description: "dense and lexical"}}})
	r.PrintDiff(DiffSummary{Files: 1, Added: 123, Removed: 45, Lines: []string{"M internal/cli/very-long-renderer-name.go"}})
	r.PrintStatus("chat | errors 0 | tokens 1200 in / 300 out")
	r.PrintLine("generic line with long content")

	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if got := cellWidth(line); got > 16 {
			t.Fatalf("line width %d exceeds 16: %q\nfull output: %q", got, line, out.String())
		}
	}
}

func TestStreamRendererAssistantChunksHonorNarrowWidth(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 16})
	r.AppendAssistantText("12")
	r.AppendAssistantText("3456中文abcdef")
	r.EndAssistant()

	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if got := cellWidth(line); got > 16 {
			t.Fatalf("assistant line width %d exceeds 16: %q", got, line)
		}
	}
}

func TestStreamRendererAssistantChunkBoundaryPreservesWideGrapheme(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 16})
	r.AppendAssistantText("123")
	r.AppendAssistantText("中abc")
	r.EndAssistant()

	got := out.String()
	if !strings.Contains(got, "中abc") || strings.Contains(got, "?") {
		t.Fatalf("wide grapheme was lost at chunk boundary: %q", got)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if width := cellWidth(line); width > 16 {
			t.Fatalf("assistant line width %d exceeds 16: %q", width, line)
		}
	}
}

func TestStreamRendererInterruptedClosesAssistantStream(t *testing.T) {
	out := &bytes.Buffer{}
	r := NewStreamRendererWithCapabilities(out, TerminalCapabilities{Width: 80})
	r.AppendAssistantText("partial")
	r.PrintInterrupted()
	r.AppendAssistantText("next")
	r.EndAssistant()

	got := out.String()
	if !strings.Contains(got, "assistant > partial\n[interrupted] Ready for new instructions.\nassistant > next\n") {
		t.Fatalf("interruption did not close and restart assistant stream: %q", got)
	}
}
