package admission_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func TestGatewayResumesFinishedTaskWithoutResettingBatch(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "fixture", "resume the same task", "")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner := admission.ActorOwner(actor)
	budget := admission.NewBudget(ledger)
	if _, err := budget.Reserve(ctx, owner, created.ID, "known-call", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Settle(ctx, owner, "known-call", 40); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Reserve(ctx, owner, created.ID, "unknown-call", 103); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.MarkUnknown(ctx, owner, "unknown-call"); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":8,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(budget, admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if err := gateway.FinishTask(ctx, actor); err != nil {
		t.Fatal(err)
	}
	before, err := budget.Inspect(ctx, owner)
	if err != nil || before.ID == "" || before.TaskID != "" || before.UsedTokens != 40 || before.ReservedTokens != 103 || !before.UnknownUsage {
		t.Fatalf("finished task lost its accounting: %+v %v", before, err)
	}
	if _, release, err := gateway.BindActor(actor, created.ID); !errors.Is(err, admission.ErrTaskClosed) {
		if release != nil {
			release()
		}
		t.Fatalf("closed task accepted a new model scope: %v", err)
	}
	if err := gateway.ResumeTask(ctx, actor); err != nil {
		t.Fatalf("explicit same-Session resume: %v", err)
	}
	resumed, err := admission.NewBudget(ledger).Inspect(ctx, owner)
	if err != nil || resumed.ID != before.ID || resumed.LimitTokens != 100_000_000 || resumed.TaskID != created.ID || resumed.TaskOwnerID != owner ||
		resumed.UsedTokens != 40 || resumed.ReservedTokens != 103 || !resumed.UnknownUsage || resumed.ActiveCalls != 0 || resumed.CostStatus != "unknown" {
		t.Fatalf("resume changed the original batch or consumption: %+v %v", resumed, err)
	}
	binding, release, err := gateway.BindActor(actor, created.ID)
	if err != nil {
		t.Fatalf("resumed task could not bind its next model call: %v", err)
	}
	defer release()
	response, err := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "resumed-call",
		PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"continue the same task"}]}`)})
	if err != nil || response.GetErrorCode() != "" {
		t.Fatalf("resumed model call failed: %s %v", response.GetErrorCode(), err)
	}
	after, err := budget.Inspect(ctx, owner)
	if err != nil || after.ID != before.ID || after.UsedTokens != 51 || after.ReservedTokens != 103 || !after.UnknownUsage || after.ActiveCalls != 0 {
		t.Fatalf("new call reset or rewrote old accounting: %+v %v", after, err)
	}
	if _, err := budget.Reserve(ctx, owner, created.ID, "known-call", 100); !errors.Is(err, admission.ErrCallReserved) {
		t.Fatalf("resume made a prior call identity dispatchable again: %v", err)
	}
}

func TestGatewayResumeRejectsStoppedUnknownToolEffects(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	created, err := session.NewWorkbench(ledger, nil).CreateWithWorkingDir(ctx, 7, "fixture", "retain unknown effect", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 1, "tool/dispatched", map[string]any{"run_id": "stopped", "tool_call_id": "shell", "tool_name": "Bash"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, created.ID, 2, "tool/result", map[string]any{"run_id": "stopped", "tool_call_id": "shell", "tool_name": "Bash", "outcome_unknown": true, "stopped": true}); err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if err := gateway.FinishTask(ctx, actor); err != nil {
		t.Fatalf("known stopped task could not close: %v", err)
	}
	before, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.ResumeTask(ctx, actor); !errors.Is(err, session.ErrSessionStateConflict) {
		t.Fatalf("physical stop authorized replay of an unknown effect: %v", err)
	}
	after, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
	if err != nil || len(after) != len(before) {
		t.Fatalf("rejected resume changed admission facts: %v", err)
	}
}

func TestTaskResumeRejectsForgedAndAbandonedAdmission(t *testing.T) {
	for _, scenario := range []string{"never closed", "foreign owner", "occupied", "duplicate", "with tokens", "abandoned"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			created, err := session.NewWorkbench(ledger, nil).CreateWithWorkingDir(ctx, 7, "fixture", scenario, "", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			actor, err := identity.Default().BindSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			owner := admission.ActorOwner(actor)
			budget := admission.NewBudget(ledger)
			gateway := admission.NewGateway(budget, admission.ProviderConfig{})
			if _, err := budget.AcquireTask(ctx, owner, created.ID); err != nil {
				t.Fatal(err)
			}
			if scenario == "abandoned" {
				facts, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ledger.Append(ctx, session.VerificationAdmissionLedgerID, int64(len(facts)), "admission/task_finished", map[string]any{"owner_id": owner, "task_id": created.ID, "purpose": "abandoned"}); err != nil {
					t.Fatal(err)
				}
			} else if scenario != "never closed" {
				if err := gateway.FinishTask(ctx, actor); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "duplicate" {
				if err := gateway.ResumeTask(ctx, actor); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "occupied" {
				if _, err := budget.AcquireTask(ctx, owner, "other-task"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "abandoned" {
				if err := gateway.ResumeTask(ctx, actor); !errors.Is(err, admission.ErrTaskClosed) {
					t.Fatalf("abandoned task resumed through ordinary admission: %v", err)
				}
				after, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
				if err != nil || len(after) != len(before) {
					t.Fatalf("rejected abandoned resume changed facts: %v", err)
				}
				return
			}
			fact := map[string]any{"owner_id": owner, "task_id": created.ID}
			if scenario == "foreign owner" {
				fact["owner_id"] = owner + 1
			}
			if scenario == "with tokens" {
				fact["tokens"] = 1
			}
			if _, err := ledger.Append(ctx, session.VerificationAdmissionLedgerID, int64(len(before)), "admission/task_resumed", fact); err != nil {
				t.Fatal(err)
			}
			if _, err := budget.Inspect(ctx, owner); !errors.Is(err, session.ErrEventIntegrity) {
				t.Fatalf("forged %s resume was accepted: %v", scenario, err)
			}
		})
	}
}

func TestConcurrentTaskResumePersistsOneAdmission(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	first, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	created, err := session.NewWorkbench(first, nil).CreateWithWorkingDir(ctx, 7, "fixture", "concurrent resume", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(first)
	firstGateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if err := firstGateway.FinishTask(ctx, actor); err != nil {
		t.Fatal(err)
	}
	before, err := budget.Inspect(ctx, admission.ActorOwner(actor))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := first.Events(ctx, session.VerificationAdmissionLedgerID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondGateway := admission.NewGateway(admission.NewBudget(second), admission.ProviderConfig{})
	start, results := make(chan struct{}), make(chan error, 2)
	for _, gateway := range []*admission.Gateway{firstGateway, secondGateway} {
		go func() {
			<-start
			results <- gateway.ResumeTask(ctx, actor)
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent explicit resume: %v", err)
		}
	}
	afterFacts, err := first.Events(ctx, session.VerificationAdmissionLedgerID)
	if err != nil || len(afterFacts) != len(facts)+1 {
		t.Fatalf("concurrent resume duplicated or lost admission: %d %v", len(afterFacts), err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := admission.NewBudget(reopened).Inspect(ctx, admission.ActorOwner(actor))
	if err != nil || after.ID != before.ID || after.TaskID != created.ID || after.TaskOwnerID != admission.ActorOwner(actor) || after.LimitTokens != 100_000_000 {
		t.Fatalf("reopen lost the original task/batch: %+v %v", after, err)
	}
}
