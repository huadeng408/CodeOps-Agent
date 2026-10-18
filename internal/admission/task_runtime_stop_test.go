package admission_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
)

type unknownRuntimeConversation struct{}

func (unknownRuntimeConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	handlers.Tool(ctx, orchestrator.ToolCall{ID: "shell", Name: "Bash", ParametersJSON: `{"command":"make test"}`})
	return orchestrator.ConversationResult{}, errors.New("runtime termination was not confirmed")
}

func TestGatewayRuntimeStopProofRejectsUnboundResults(t *testing.T) {
	for _, scenario := range []string{"valid proof", "checksum", "runtime", "missing result", "missing run", "duplicate reference", "empty proof", "version"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			workbench := session.NewWorkbench(ledger, nil)
			created, err := workbench.Create(ctx, 7, "fixture", "stop proof", "")
			if err != nil {
				t.Fatal(err)
			}
			actor, err := identity.Default().BindSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "missing run" {
				if _, err := ledger.Append(ctx, created.ID, 1, "tool/dispatched", map[string]any{"run_id": "run", "tool_call_id": "shell", "tool_name": "Bash"}); err != nil {
					t.Fatal(err)
				}
				if _, err := ledger.Append(ctx, created.ID, 2, "tool/result", map[string]any{"run_id": "run", "tool_call_id": "shell", "tool_name": "Bash", "runtime_id": "retained-runtime", "outcome_unknown": true}); err != nil {
					t.Fatal(err)
				}
			} else {
				runner := session.NewSessionRunner(workbench, unknownRuntimeConversation{}, session.ToolExecutionFunc(func(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
					return orchestrator.ToolResult{RuntimeID: "retained-runtime", OutcomeUnknown: true, ExitCode: 126}
				}), session.SessionRunnerOptions{})
				defer runner.Close()
				if _, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "run", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "test", Actor: actor}); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(5 * time.Second)
				for {
					view, err := workbench.Get(ctx, 7, created.ID)
					if err != nil {
						t.Fatal(err)
					}
					if view.Run != nil && view.Run.Status == session.RunFailed {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("proof fixture did not reach a terminal run")
					}
					time.Sleep(time.Millisecond)
				}
			}
			before, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			var result session.Event
			for _, event := range before.Events {
				if event.Type == "tool/result" {
					result = event
				}
			}
			if result.EventID == "" {
				t.Fatal("proof fixture has no source result")
			}
			ref := map[string]string{"event_id": result.EventID, "checksum": result.Checksum, "runtime_id": "retained-runtime"}
			proof := map[string]any{"schema_version": 1, "results": []any{ref}}
			switch scenario {
			case "checksum":
				ref["checksum"] = strings.Repeat("0", 64)
			case "runtime":
				ref["runtime_id"] = "another-runtime"
			case "missing result":
				ref["event_id"] = "absent"
			case "duplicate reference":
				proof["results"] = []any{ref, ref}
			case "empty proof":
				proof["results"] = []any{}
			case "version":
				proof["schema_version"] = 2
			}
			if _, err := ledger.Append(ctx, created.ID, int64(len(before.Events)), "tool/runtime-stopped", proof); err != nil {
				t.Fatal(err)
			}
			gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{})
			if err := gateway.FinishTask(ctx, actor); scenario == "valid proof" {
				if err != nil {
					t.Fatalf("valid proof fixture cannot finish: %v", err)
				}
			} else if !errors.Is(err, session.ErrEventIntegrity) {
				t.Fatalf("invalid stop proof released the task: %v", err)
			}
			snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
			if err != nil || len(snapshot.Events) != len(before.Events)+1 {
				t.Fatal("invalid proof caused another write")
			}
		})
	}
}

func TestGatewayExplicitFinishConfirmsStoppedRuntimeWithoutChangingUnknownOutcome(t *testing.T) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "fixture", "unknown runtime", "")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner := session.NewSessionRunner(workbench, unknownRuntimeConversation{}, session.ToolExecutionFunc(func(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
		return orchestrator.ToolResult{Output: "partial tests", ExitCode: 126, Error: "unknown execution", RuntimeID: "retained-runtime", OutcomeUnknown: true}
	}), session.SessionRunnerOptions{})
	defer runner.Close()
	run, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "run", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "test", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := workbench.Get(ctx, 7, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Run != nil && view.Run.RunID == run.RunID && view.Run.Status == session.RunFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unknown tool run did not terminate")
		}
		time.Sleep(time.Millisecond)
	}
	budget := admission.NewBudget(ledger)
	if _, err := budget.AcquireTask(ctx, admission.ActorOwner(actor), created.ID); err != nil {
		t.Fatal(err)
	}
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if err := gateway.FinishTask(ctx, actor); !errors.Is(err, session.ErrSessionStateConflict) {
		t.Fatal("unconfirmed runtime did not fence task completion")
	}
	before, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	observerUnavailable := errors.New("runtime observer unavailable")
	gateway.SetRuntimeStopVerifier(func(context.Context, string) error { return observerUnavailable })
	if err := gateway.FinishTask(ctx, actor); !errors.Is(err, observerUnavailable) {
		t.Fatal("unavailable observer released the task")
	}
	unchanged, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil || len(unchanged.Events) != len(before.Events) {
		t.Fatal("failed stop observation appended a confirmation")
	}
	checks := 0
	gateway.SetRuntimeStopVerifier(func(_ context.Context, runtimeID string) error {
		if runtimeID != "retained-runtime" {
			t.Fatalf("completion checked another runtime: %s", runtimeID)
		}
		checks++
		return nil
	})
	if err := gateway.FinishTask(ctx, actor); err != nil {
		t.Fatalf("verified absent runtime still wedged explicit Finish: %v", err)
	}
	if err := gateway.FinishTask(ctx, actor); err != nil {
		t.Fatal(err)
	}
	after, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil || checks != 1 || len(after.Events) != len(before.Events)+1 {
		t.Fatalf("stop confirmation was missing or replayed: checks=%d err=%v", checks, err)
	}
	for i, event := range before.Events {
		if after.Events[i].Checksum != event.Checksum {
			t.Fatal("completion rewrote original execution facts")
		}
		if event.Type == "tool/result" {
			var result map[string]any
			if json.Unmarshal(after.Events[i].Payload, &result) != nil || result["outcome_unknown"] != true || result["output"] != "partial tests" {
				t.Fatal("stop confirmation promoted an unknown command to a known result")
			}
		}
	}
	batch, err := budget.Inspect(ctx, admission.ActorOwner(actor))
	if err != nil || batch.TaskID != "" || batch.UsedTokens != 0 || batch.ReservedTokens != 0 {
		t.Fatalf("Finish did not release only the task slot: %+v %v", batch, err)
	}
}
