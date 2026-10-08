package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"code-agent/internal/orchestrator"
)

const codeModifiedEventType = "code/modified"

// CodeModificationView contains proof of a file transition without copying
// either side of the file into the browser event stream.
type CodeModificationView struct {
	RunID        string `json:"runId"`
	ToolCallID   string `json:"toolCallId"`
	ToolName     string `json:"toolName"`
	Path         string `json:"path"`
	Operation    string `json:"operation"`
	Summary      string `json:"summary"`
	BeforeSHA256 string `json:"beforeSha256"`
	AfterSHA256  string `json:"afterSha256"`
	DiffSHA256   string `json:"diffSha256"`
}

type codeModificationPayload struct {
	RunID        string `json:"run_id"`
	ToolCallID   string `json:"tool_call_id"`
	ToolName     string `json:"tool_name"`
	Path         string `json:"path"`
	Operation    string `json:"operation"`
	Summary      string `json:"summary"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
	DiffSHA256   string `json:"diff_sha256"`
	Before       string `json:"before,omitempty"`
	After        string `json:"after,omitempty"`
}

func codeModificationPayloads(result orchestrator.ToolResult, runID string) []codeModificationPayload {
	if result.Error != "" || result.ExitCode != 0 || (result.ToolName != "Write" && result.ToolName != "Edit" && result.ToolName != "NotebookEdit") {
		return nil
	}
	out := make([]codeModificationPayload, 0, len(result.Changes))
	for _, change := range result.Changes {
		path := filepath.ToSlash(strings.TrimSpace(change.Path))
		payload := codeModificationPayload{
			RunID: runID, ToolCallID: result.ToolCallID, ToolName: result.ToolName,
			Path: path, Operation: result.ToolName, Summary: result.ToolName + " modified " + path,
			BeforeSHA256: hashCodeState(change.Before), AfterSHA256: hashCodeState(change.After),
			DiffSHA256: hashCodeTransition(change.Before, change.After),
			Before:     change.Before, After: change.After,
		}
		if validateCodeModificationPayload(payload) == nil {
			out = append(out, payload)
		}
	}
	return out
}

func codeModificationEventView(event Event) (CodeModificationView, error) {
	var payload codeModificationPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil || validateCodeModificationPayload(payload) != nil {
		return CodeModificationView{}, fmt.Errorf("%w: invalid code modification payload at seq %d", ErrEventIntegrity, event.Seq)
	}
	return CodeModificationView{
		RunID: payload.RunID, ToolCallID: payload.ToolCallID, ToolName: payload.ToolName,
		Path: payload.Path, Operation: payload.Operation, Summary: payload.Summary,
		BeforeSHA256: payload.BeforeSHA256, AfterSHA256: payload.AfterSHA256, DiffSHA256: payload.DiffSHA256,
	}, nil
}

func validateCodeModificationPayload(payload codeModificationPayload) error {
	if strings.TrimSpace(payload.RunID) == "" || strings.TrimSpace(payload.ToolCallID) == "" || strings.TrimSpace(payload.ToolName) == "" || strings.TrimSpace(payload.Path) == "" {
		return fmt.Errorf("code modification identity is required")
	}
	if payload.Operation != payload.ToolName || strings.TrimSpace(payload.Summary) == "" {
		return fmt.Errorf("code modification operation is invalid")
	}
	// Ledger paths use portable slash-separated names, independent of the host OS.
	if !fs.ValidPath(payload.Path) || payload.Path == "." || strings.ContainsAny(payload.Path, "\\\x00\r\n:") {
		return fmt.Errorf("code modification path must stay within workspace")
	}
	for _, digest := range []string{payload.BeforeSHA256, payload.AfterSHA256, payload.DiffSHA256} {
		if len(digest) != sha256.Size*2 {
			return fmt.Errorf("code modification digest is invalid")
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return fmt.Errorf("code modification digest is invalid")
		}
	}
	return nil
}

func hashCodeState(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func hashCodeTransition(before, after string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(before))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(after))
	return hex.EncodeToString(hash.Sum(nil))
}
