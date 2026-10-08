package session

import (
	"testing"

	"code-agent/internal/orchestrator"
)

func TestValidateCodeModificationPayloadRejectsEscapingPaths(t *testing.T) {
	valid := codeModificationPayload{
		RunID: "run", ToolCallID: "call", ToolName: "Write", Operation: "Write", Summary: "Write file",
		BeforeSHA256: hashCodeState("before"), AfterSHA256: hashCodeState("after"), DiffSHA256: hashCodeTransition("before", "after"),
	}
	for _, path := range []string{
		"../outside.txt", `C:\outside.txt`, "C:/outside.txt", "C:outside.txt",
		`\\server\share\outside.txt`, `\outside.txt`, "/tmp/outside.txt", "..", ".",
		`..\outside.txt`, "src/../../outside.txt", `src\..\..\outside.txt`,
		"src/a.txt:stream", "src/a\x00.txt", "src/a\n.txt", "src/a\r.txt",
	} {
		valid.Path = path
		if err := validateCodeModificationPayload(valid); err == nil {
			t.Fatalf("expected path rejection for %q", path)
		}
		result := orchestrator.ToolResult{ToolName: "Write", ToolCallID: "call", Changes: []orchestrator.CodeChange{{Path: path, Before: "before", After: "after"}}}
		if got := codeModificationPayloads(result, "run"); len(got) != 0 {
			t.Fatalf("unsafe path produced a modification receipt: %q", path)
		}
	}
	for _, path := range []string{"src/example.txt", "src/中文.txt", "src/foo..bar.txt"} {
		valid.Path = path
		if err := validateCodeModificationPayload(valid); err != nil {
			t.Fatalf("valid relative path %q rejected: %v", path, err)
		}
	}
}

func TestCodeModificationPayloadRetainsContentForBackendRestore(t *testing.T) {
	result := orchestrator.ToolResult{ToolName: "Write", ToolCallID: "call", Changes: []orchestrator.CodeChange{{Path: "src/a.txt", Before: "before", After: "after"}}}
	payloads := codeModificationPayloads(result, "run")
	if len(payloads) != 1 {
		t.Fatalf("payload count = %d, want 1", len(payloads))
	}
	if payloads[0].Before != "before" || payloads[0].After != "after" {
		t.Fatalf("payload content = before=%q after=%q", payloads[0].Before, payloads[0].After)
	}
}
