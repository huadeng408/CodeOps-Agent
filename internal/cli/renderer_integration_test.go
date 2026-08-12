package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRendererPipeTranscriptIsLinearAndSafe(t *testing.T) {
	reader, writer := io.Pipe()
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
