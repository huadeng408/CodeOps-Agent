package cli_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/cli"
	"code-agent/internal/config"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func TestEvaluationYieldPreservesExpiredSessionAndSharedAccounting(t *testing.T) {
	for _, scenario := range []string{"expired", "live lease", "pending tool", "prepared tool", "unknown tool", "unknown result", "wrong actor", "stale session", "stale admission", "not enabled"} {
		t.Run(scenario, func(t *testing.T) { testEvaluationYield(t, scenario) })
	}
}

func testEvaluationYield(t *testing.T, scenario string) {
	t.Setenv("CODE_AGENT_SESSION_LEDGER_PATH", "")
	t.Setenv("CODE_AGENT_EVAL_ALLOW_TASK_YIELD", "1")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_MODEL", "approved-model")
	ctx := context.Background()
	workspace := t.TempDir()
	if body, err := exec.Command("git", "-C", workspace, "init").CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v %s", err, body)
	}
	cfg := config.Default(workspace)
	// Yield must use neither the orchestrator nor a product recovery bootstrap.
	cfg.OrchestratorCommand = "must-not-launch-an-orchestrator-for-yield"
	ledger, err := session.OpenSQLiteEventLog(cfg.SessionDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "unfinished original task", "resume later", workspace)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := workbench.AppendUserMessage(ctx, 7, created.ID, 1, "original task input")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workbench.CreateCheckpoint(ctx, 7, created.ID, 2, message.ID, "original")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	messageFact := snapshot.Events[1]
	surfaceHash := sha256.Sum256([]byte(messageFact.Checksum + "\x00"))
	_, err = ledger.Append(ctx, created.ID, 3, "session/continued", map[string]any{
		"run_id": "original-run", "request_id": "original-request", "checkpoint_hash": checkpoint.Hash,
		"target_event_id": messageFact.EventID, "target_seq": messageFact.Seq, "target_checksum": messageFact.Checksum,
		"surface_sha256": hex.EncodeToString(surfaceHash[:]), "input_event_id": messageFact.EventID, "resume_count": 1, "actor": actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	leaseUntil := time.Now().Add(-time.Minute)
	if scenario == "live lease" {
		leaseUntil = time.Now().Add(time.Minute)
	}
	_, err = ledger.Append(ctx, created.ID, 4, "session/run-leased", map[string]any{
		"run_id": "original-run", "request_id": "original-request", "lease_id": "original-lease", "worker_id": "original-worker", "attempt": 1, "lease_until": leaseUntil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scenario == "pending tool" || scenario == "prepared tool" || scenario == "unknown tool" {
		typ := "tool/dispatched"
		if scenario == "prepared tool" {
			typ = "tool/call"
		}
		if scenario == "unknown tool" {
			typ = "tool/unknown"
		}
		_, err = ledger.Append(ctx, created.ID, 5, typ, map[string]any{"run_id": "original-run", "tool_call_id": "original-write", "tool_name": "Write", "arguments_json": `{"path":"original.txt","content":"pending"}`})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "unknown result" {
		_, err = ledger.Append(ctx, created.ID, 5, "tool/result", map[string]any{"run_id": "original-run", "tool_call_id": "original-write", "tool_name": "Write", "outcome_unknown": true, "stopped": true})
		if err != nil {
			t.Fatal(err)
		}
	}
	budget := admission.NewBudget(ledger)
	owner := admission.ActorOwner(actor)
	if _, err := budget.Reserve(ctx, owner, created.ID, "historical-unknown", 123); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.MarkUnknown(ctx, owner, "historical-unknown"); err != nil {
		t.Fatal(err)
	}
	before, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	admissionBefore, err := session.ReadVerifiedSnapshot(ctx, ledger, session.VerificationAdmissionLedgerID)
	if err != nil {
		t.Fatal(err)
	}
	request := cli.EvaluationRequest{SchemaVersion: 1, RunID: "yield-review", TaskID: "original-task", SessionID: created.ID, Operation: "yield", DryRun: true,
		Workspace: workspace, Message: "Yield admission without continuing or finalizing the original task", Model: "approved-model",
		Budget: cli.EvaluationBudget{MaxTokens: 1000, WallClockSeconds: 15}, ExpectedSessionHeadChecksum: before.Events[len(before.Events)-1].Checksum}
	operator := actor
	if scenario == "wrong actor" {
		operator.Subject = "different-operator"
	}
	actorJSON, _ := json.Marshal(operator)
	t.Setenv("CODE_AGENT_EVAL_YIELD_ACTOR_JSON", string(actorJSON))
	if scenario == "stale session" {
		request.ExpectedSessionHeadChecksum = strings.Repeat("0", 64)
	}
	if scenario == "stale admission" {
		request.ExpectedAdmissionHeadChecksum = strings.Repeat("0", 64)
	}
	if scenario == "not enabled" {
		t.Setenv("CODE_AGENT_EVAL_ALLOW_TASK_YIELD", "")
	}
	run := func() (map[string]any, error) {
		input, _ := json.Marshal(request)
		var output strings.Builder
		err := cli.RunEvaluation(ctx, cfg, strings.NewReader(string(input)), &output, io.Discard)
		var receipt map[string]any
		if decodeErr := json.Unmarshal([]byte(strings.TrimSpace(output.String())), &receipt); decodeErr != nil {
			t.Fatalf("operator protocol: %v", decodeErr)
		}
		return receipt, err
	}
	review, runErr := run()
	if scenario != "expired" {
		if runErr == nil {
			t.Fatalf("unsafe yield accepted: %s %+v", scenario, review)
		}
	} else {
		if runErr != nil || review["task_state"] != "yieldable" || review["session_unchanged"] != true || review["accounting_retained"] != true || review["requires_updated_reader"] != true {
			t.Fatalf("read-only review failed: %v %+v", runErr, review)
		}
	}
	afterReview, _ := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	admissionAfterReview, _ := session.ReadVerifiedSnapshot(ctx, ledger, session.VerificationAdmissionLedgerID)
	if len(before.Events) != len(afterReview.Events) || before.Events[len(before.Events)-1].Checksum != afterReview.Events[len(afterReview.Events)-1].Checksum || len(admissionBefore.Events) != len(admissionAfterReview.Events) {
		t.Fatal("dry-run or refusal wrote canonical facts")
	}
	if scenario != "expired" {
		return
	}
	if err := admission.NewGateway(budget, admission.ProviderConfig{}).FinishTask(ctx, actor); !errors.Is(err, session.ErrSessionStateConflict) {
		t.Fatal("yieldable unfinished run was incorrectly considered finished")
	}
	request.DryRun = false
	request.ExpectedAdmissionHeadChecksum = admissionBefore.Events[len(admissionBefore.Events)-1].Checksum
	receipt, err := run()
	if err != nil || receipt["task_state"] != "yielded" || receipt["session_unchanged"] != true || receipt["admission_boundary_verified"] != true {
		t.Fatalf("operator yield failed: %v %+v", err, receipt)
	}
	batch, err := budget.Inspect(ctx, owner)
	if err != nil || batch.TaskID != "" || batch.ReservedTokens != 123 || !batch.UnknownUsage || batch.ActiveCalls != 0 {
		t.Fatalf("yield changed historical accounting: %+v %v", batch, err)
	}
	if _, err := budget.AcquireTask(ctx, owner, "independent-task"); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.FinishTask(ctx, owner, "independent-task"); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.AcquireTask(ctx, owner, created.ID); err != nil {
		t.Fatalf("yield permanently closed the original identity: %v", err)
	}
	after, _ := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if len(after.Events) != len(before.Events) || after.Events[len(after.Events)-1].Checksum != before.Events[len(before.Events)-1].Checksum {
		t.Fatal("yield or reacquire rewrote original Session")
	}
}
