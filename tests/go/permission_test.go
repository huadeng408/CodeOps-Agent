package codeagent_test

import (
	"testing"

	"code-agent/internal/permission"
)

func TestDefaultPermissions(t *testing.T) {
	ctrl := permission.NewController(nil, nil)

	if got := ctrl.Check("Read", nil); got != permission.Approve {
		t.Fatalf("Read should auto-approve, got %v", got)
	}
	if got := ctrl.Check("Bash", nil); got != permission.AskUser {
		t.Fatalf("Bash should ask, got %v", got)
	}
}

func TestDenyRulesTakePrecedence(t *testing.T) {
	ctrl := permission.NewControllerWithRules(
		nil,
		[]permission.AllowRule{{Tool: "Bash", Pattern: "go test.*"}},
		[]permission.AllowRule{{Tool: "Bash", Pattern: "rm -rf"}},
	)

	if got := ctrl.Check("Bash", map[string]any{"command": "rm -rf /tmp/demo"}); got != permission.Deny {
		t.Fatalf("deny rule should win, got %v", got)
	}
	if got := ctrl.Check("Bash", map[string]any{"command": "go test ./..."}); got != permission.Approve {
		t.Fatalf("allow rule should approve, got %v", got)
	}
}

func TestSessionApproval(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	if got := ctrl.Check("Write", nil); got != permission.AskUser {
		t.Fatalf("Write should initially ask, got %v", got)
	}

	ctrl.ApproveSession("Write")
	if got := ctrl.Check("Write", nil); got != permission.Approve {
		t.Fatalf("Write should approve after session approval, got %v", got)
	}
}

func TestUnknownToolsAskForApproval(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	if got := ctrl.Check("MCPTool", nil); got != permission.AskUser {
		t.Fatalf("unknown tools should ask, got %v", got)
	}
	if got := ctrl.Level("MCPTool"); got != permission.AskSession {
		t.Fatalf("unknown tools should default to ask-session, got %v", got)
	}
}

func TestSessionApprovalSnapshotAndRestore(t *testing.T) {
	ctrl := permission.NewController(nil, nil)
	ctrl.ApproveSession("Write")
	ctrl.ApproveSession("Git")
	ctrl.ApproveSession("")

	approved := ctrl.ApprovedTools()
	if len(approved) != 2 || approved[0] != "Git" || approved[1] != "Write" {
		t.Fatalf("unexpected approved tools: %#v", approved)
	}

	restored := permission.NewController(nil, nil)
	restored.RestoreApprovedTools([]string{"Write", "Write"})
	if got := restored.Check("Write", nil); got != permission.Approve {
		t.Fatalf("restored Write should approve, got %v", got)
	}
	if got := restored.Check("Git", nil); got != permission.AskUser {
		t.Fatalf("Git should not be restored, got %v", got)
	}
}
