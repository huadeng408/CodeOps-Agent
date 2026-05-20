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
