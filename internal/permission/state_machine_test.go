package permission

import (
	"testing"
	"time"

	"code-agent/internal/identity"
)

func TestThreeLevelSignedAuthorizationStateMachine(t *testing.T) {
	controller := NewController(nil, nil)
	actor, err := identity.Default().BindSession("session-auth-sm-1")
	if err != nil {
		t.Fatalf("bind actor: %v", err)
	}

	// L0 (Read) auto-allows and transitions directly to StateGrantedL0.
	readParams := map[string]any{"path": "README.md"}
	resL0 := controller.EvaluateWithGrant(actor, "Read", readParams, nil)
	if resL0.Decision != Approve || resL0.State != StateGrantedL0 || resL0.Tier != TierL0AutoAllow {
		t.Fatalf("expected L0 Read to auto-approve, got %+v", resL0)
	}

	// L1 (Edit) without grant requires session approval.
	editParams := map[string]any{"path": "internal/safety/analyzer.go"}
	resL1NoGrant := controller.EvaluateWithGrant(actor, "Edit", editParams, nil)
	if resL1NoGrant.Decision != AskUser || resL1NoGrant.State != StatePendingApproval || resL1NoGrant.Tier != TierL1SessionSigned {
		t.Fatalf("expected L1 Edit without grant to require approval, got %+v", resL1NoGrant)
	}

	// Issue a signed L1 session grant and verify it approves L1 Edit.
	l1Grant, err := controller.IssueSignedGrant(actor, "Edit", editParams, TierL1SessionSigned, 10*time.Minute)
	if err != nil {
		t.Fatalf("issue L1 grant: %v", err)
	}
	resL1WithGrant := controller.EvaluateWithGrant(actor, "Edit", editParams, l1Grant)
	if resL1WithGrant.Decision != Approve || resL1WithGrant.State != StateGrantedL1Session {
		t.Fatalf("expected valid L1 signed grant to approve Edit, got %+v", resL1WithGrant)
	}

	// High-risk L2 operation (Bash) must intercept even if presented with an L1 grant.
	bashParams := map[string]any{"command": "git status --short"}
	resL2WithL1Grant := controller.EvaluateWithGrant(actor, "Bash", bashParams, l1Grant)
	if resL2WithL1Grant.Decision == Approve {
		t.Fatal("expected L2 high-risk Bash to reject L1 grant")
	}

	// Issue an explicit L2 manual-approval signed grant bound to exact params.
	l2Grant, err := controller.IssueSignedGrant(actor, "Bash", bashParams, TierL2HighRiskManualIntercept, 5*time.Minute)
	if err != nil {
		t.Fatalf("issue L2 grant: %v", err)
	}
	resL2Approved := controller.EvaluateWithGrant(actor, "Bash", bashParams, l2Grant)
	if resL2Approved.Decision != Approve || resL2Approved.State != StateGrantedL2HighRisk {
		t.Fatalf("expected valid L2 signed grant to approve Bash, got %+v", resL2Approved)
	}

	// Parameter tampering (command changed after manual approval) must be rejected!
	tamperedParams := map[string]any{"command": "git status --short && rm -rf /"}
	resL2TamperedParams := controller.EvaluateWithGrant(actor, "Bash", tamperedParams, l2Grant)
	if resL2TamperedParams.Decision != Deny || resL2TamperedParams.State != StateDeniedTampered {
		t.Fatalf("expected L2 grant with modified command parameters to be denied as tampered, got %+v", resL2TamperedParams)
	}

	// Signature tampering must be rejected!
	forgedGrant := *l2Grant
	forgedGrant.HMACSignature = "0000000000000000000000000000000000000000000000000000000000000000"
	resL2Forged := controller.EvaluateWithGrant(actor, "Bash", bashParams, &forgedGrant)
	if resL2Forged.Decision != Deny || resL2Forged.State != StateDeniedTampered {
		t.Fatalf("expected forged HMAC signature to be denied, got %+v", resL2Forged)
	}
}
