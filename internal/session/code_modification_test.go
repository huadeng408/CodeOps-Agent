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
	for _, path := range []string{"../outside.txt", `C:\outside.txt`, "/tmp/outside.txt", ".."} {
		valid.Path = path
		if err := validateCodeModificationPayload(valid); err == nil {
			t.Fatalf("expected path rejection for %q", path)
		}
	}
	valid.Path = "src/example.txt"
	if err := validateCodeModificationPayload(valid); err != nil {
		t.Fatalf("valid relative path rejected: %v", err)
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
