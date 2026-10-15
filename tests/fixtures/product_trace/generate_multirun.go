// Generate contract fixtures through the public Runner and Ledger seams.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
)

type conversation struct{}

func (conversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if request.Input != "readonly fixture" && request.Input != "unadmitted fixture" {
		handlers.Tool(ctx, orchestrator.ToolCall{ID: "tool-1", Name: "Write", ParametersJSON: `{"path":"fixture.go","content":"package fixture\n"}`})
	}
	return orchestrator.ConversationResult{Success: true, Message: "fixture completed"}, nil
}

func main() {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(os.Args[1], "fixture.sqlite"))
	must(err)
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "fixture", "trace fixture", "")
	must(err)
	actor := identity.Actor{SchemaVersion: 1, ActorID: "actor:local", Subject: "local", TenantID: "tenant:local", Roles: []string{"LOCAL"}, SessionID: created.ID}
	runner := session.NewSessionRunner(workbench, conversation{}, session.ToolExecutionFunc(func(_ context.Context, _ identity.Actor, _ string, _ orchestrator.ToolCall) orchestrator.ToolResult {
		return orchestrator.ToolResult{Output: "fixture", Changes: []orchestrator.CodeChange{{Path: "fixture.go", After: "package fixture\n"}}}
	}), session.SessionRunnerOptions{})
	defer runner.Close()
	inputs := []string{"coding fixture", "readonly fixture"}
	if len(os.Args) > 2 && os.Args[2] == "repeat-tool" {
		inputs[1] = "coding follow-up fixture"
	}
	if len(os.Args) > 2 && os.Args[2] == "unadmitted-run" {
		inputs = append(inputs, "unadmitted fixture")
	}
	for _, input := range inputs {
		for {
			view, err := workbench.Get(ctx, 7, created.ID)
			must(err)
			_, err = runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: input, SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(view.EventCount), Content: input, Actor: actor})
			// Completion progress can advance the cursor before the next submission.
			if errors.Is(err, session.ErrSequenceConflict) {
				must(ctx.Err())
				continue
			}
			must(err)
			break
		}
		for {
			view, err := workbench.Get(ctx, 7, created.ID)
			must(err)
			if view.Run != nil && view.Run.Status == session.RunCompleted {
				break
			}
			must(ctx.Err())
			time.Sleep(time.Millisecond)
		}
	}
	write := func(name, id string) {
		snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, id)
		must(err)
		data, err := json.Marshal(map[string]any{"events": snapshot.Events})
		must(err)
		must(os.WriteFile(filepath.Join(os.Args[1], name+".json"), data, 0600))
	}
	write("session", created.ID)
	budget, owner := admission.NewBudget(ledger), admission.ActorOwner(actor)
	_, err = budget.Open(ctx, owner)
	must(err)
	write("before", session.VerificationAdmissionLedgerID)
	for _, callID := range []string{"call-1", "call-2"} {
		events, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
		must(err)
		// Gateway foreground facts include purpose; Budget.Reserve has no purpose parameter.
		_, err = ledger.Append(ctx, session.VerificationAdmissionLedgerID, int64(len(events)), "admission/call_reserved", map[string]any{"owner_id": owner, "task_id": created.ID, "call_id": callID, "purpose": "foreground", "tokens": 100})
		must(err)
		_, err = budget.Settle(ctx, owner, callID, 20)
		must(err)
	}
	_, err = budget.FinishTask(ctx, owner, created.ID)
	must(err)
	write("after", session.VerificationAdmissionLedgerID)
}
