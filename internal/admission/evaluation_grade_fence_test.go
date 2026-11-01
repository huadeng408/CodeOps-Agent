package admission_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/memory"
	"code-agent/internal/session"
)

func TestGatewayRetainsTaskForPendingOrUnstoppedGrade(t *testing.T) {
	for _, resultPresent := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending-intent", true: "unknown-not-stopped"}[resultPresent], func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "canonical.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			actor, _ := identity.Default().BindSession("owner")
			owner, err := memory.OwnerForActor(actor)
			if err != nil {
				t.Fatal(err)
			}
			view, err := session.NewWorkbench(ledger, nil).CreateWithWorkingDir(ctx, owner, "fixture", "lost remote grade result", "", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			actor = identity.Default()
			actor, err = actor.BindSession(view.ID)
			if err != nil {
				t.Fatal(err)
			}
			policy := session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000}
			if err := session.BindEvaluation(ctx, ledger, view.ID, session.EvaluationBinding{SchemaVersion: 1, RunID: "grade-fence", TaskID: "grade-fence", Policy: policy}); err != nil {
				t.Fatal(err)
			}
			events, err := ledger.Events(ctx, view.ID)
			if err != nil {
				t.Fatal(err)
			}
			head := events[len(events)-1].Checksum
			pin := strings.Repeat("a", 64)
			grade := session.EvaluationGradeView{State: "unknown", CandidateSHA256: pin, ChecksSHA256: pin, CheckerSHA256: pin, OperationID: strings.Repeat("b", 64)}
			payload := map[string]any{"schema_version": 1, "owner_id": owner, "actor": actor, "request_id": "grade-control-fixture", "binding_checksum": head, "source_head_checksum": head, "proposal_event_id": "fixture", "proposal_event_checksum": pin, "assertion_count": 1, "view": grade}
			intent, err := ledger.Append(ctx, view.ID, int64(len(events)), "evaluation/grade-intent", payload)
			if err != nil {
				t.Fatal(err)
			}
			if resultPresent {
				grade.IntentEventID, grade.IntentEventChecksum = intent.EventID, intent.Checksum
				grade.RuntimeID = grade.OperationID + "/11111111-2222-4333-8444-555555555555/" + strings.Repeat("c", 64)
				payload["view"] = grade
				if _, err := ledger.Append(ctx, view.ID, int64(len(events)+1), "evaluation/grade-result", payload); err != nil {
					t.Fatal(err)
				}
			}
			budget := admission.NewBudget(ledger)
			if _, err := budget.AcquireTask(ctx, admission.ActorOwner(actor), view.ID); err != nil {
				t.Fatal(err)
			}
			before, err := session.ReadVerifiedSnapshot(ctx, ledger, view.ID)
			if err != nil {
				t.Fatal(err)
			}
			gateway := admission.NewGateway(budget, admission.ProviderConfig{})
			for _, release := range []func(context.Context, identity.Actor) error{gateway.FinishTask, gateway.YieldTask, gateway.AbandonCanceledTask} {
				if err := release(ctx, actor); err == nil {
					t.Fatal("remote grade uncertainty released admission")
				}
			}
			batch, err := budget.Inspect(ctx, admission.ActorOwner(actor))
			if err != nil || batch.TaskID != view.ID || batch.ActiveCalls != 0 || batch.UsedTokens != 0 {
				t.Fatal("refused controls changed the task or accounting")
			}
			after, err := session.ReadVerifiedSnapshot(ctx, ledger, view.ID)
			if err != nil || len(before.Events) != len(after.Events) || before.Events[len(before.Events)-1].Checksum != after.Events[len(after.Events)-1].Checksum {
				t.Fatal("refused controls rewrote grade facts")
			}
		})
	}
}
