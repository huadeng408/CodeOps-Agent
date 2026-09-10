package session

import "testing"

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
