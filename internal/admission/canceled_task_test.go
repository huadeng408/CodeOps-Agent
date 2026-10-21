package admission_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
)

type canceledAdmissionConversation struct{ started chan struct{} }

func (conversation canceledAdmissionConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if request.Input == "earlier" {
		result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "read", Name: "Read", ParametersJSON: `{}`})
		return orchestrator.ConversationResult{Success: result.Error == "", Message: "earlier result"}, nil
	}
	close(conversation.started)
	<-ctx.Done()
	return orchestrator.ConversationResult{}, ctx.Err()
}

func TestAbandonCanceledAdmissionPreservesForeignCleanupUnknown(t *testing.T) {
	for _, scenario := range []string{"canceled", "missing cancel", "active call", "live scope", "missing tool result", "unconfirmed tool stop", "foreign actor"} {
		t.Run(scenario, func(t *testing.T) { testCanceledAdmission(t, scenario) })
	}
}

func TestAbandonCanceledAdmissionRetainsMissingFileReadOutcome(t *testing.T) {
	testCanceledAdmission(t, "missing file read result")
}

func TestAbandonCanceledAdmissionRejectsUnverifiedFileReads(t *testing.T) {
	for _, suffix := range []string{"PDF", "PDF alias", "MCP", "unconfirmed runtime", "mismatched dispatch", "duplicate dispatch", "missing preparation", "missing closure", "wrong closure", "uncanceled history", "late dispatch", "active call", "live scope"} {
		t.Run(suffix, func(t *testing.T) { testCanceledAdmission(t, "missing file read result: "+suffix) })
	}
}

func testCanceledAdmission(t *testing.T, scenario string) {
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	workbench := session.NewWorkbench(ledger, nil)
	created, err := workbench.Create(ctx, 7, "fixture", "retire canceled admission", "")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversation := canceledAdmissionConversation{started: make(chan struct{})}
	read := session.ToolExecutionFunc(func(_ context.Context, _ identity.Actor, _ string, call orchestrator.ToolCall) orchestrator.ToolResult {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: "known read result"}
	})
	first := session.NewSessionRunner(workbench, conversation, read, session.SessionRunnerOptions{WorkerID: "earlier-process"})
	t.Cleanup(func() { _ = first.Close() })
	earlierRun, err := first.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "earlier", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(created.EventCount), Content: "earlier", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	waitView := func(want func(session.RunView) bool) session.SessionView {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			view, err := workbench.Get(ctx, 7, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Run != nil && want(*view.Run) {
				return view
			}
			if time.Now().After(deadline) {
				t.Fatalf("run did not reach expected state: %+v", view.Run)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitView(func(run session.RunView) bool { return run.Status == session.RunCompleted })
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	view, err := workbench.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	second := session.NewSessionRunner(workbench, conversation, read, session.SessionRunnerOptions{WorkerID: "current-process"})
	defer second.Close()
	run, err := second.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "current", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(view.EventCount), Content: "hold", Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-conversation.started:
	case <-time.After(5 * time.Second):
		t.Fatal("current worker did not start")
	}
	if strings.HasSuffix(scenario, "late dispatch") {
		if _, err := second.CancelRun(ctx, session.CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.HasPrefix(scenario, "missing file read result") {
		readRunID := run.RunID
		if strings.HasSuffix(scenario, "uncanceled history") {
			readRunID = earlierRun.RunID
		}
		call := map[string]any{"run_id": readRunID, "tool_call_id": "lost-read", "tool_name": "Read", "arguments_json": `{"path":"source.go"}`}
		if strings.HasSuffix(scenario, "PDF") {
			call["arguments_json"] = `{"path":"document.PDF"}`
		}
		if strings.HasSuffix(scenario, "PDF alias") {
			call["arguments_json"] = `{"PATH":"source.go","file":"folder\\..\\document.PDF"}`
		}
		if strings.HasSuffix(scenario, "MCP") {
			call["mcp_binding"] = map[string]any{"name": "Read", "server": "external"}
		}
		commit := maps.Clone(call)
		commit["commit_id"] = readRunID + "\x00lost-read"
		var pending session.Event
		for _, kind := range []string{"tool/call", "commit_pending", "tool/dispatched", "tool/unknown", "commit_done"} {
			if kind == "tool/call" && strings.HasSuffix(scenario, "missing preparation") || kind == "commit_done" && strings.HasSuffix(scenario, "missing closure") {
				continue
			}
			events, err := ledger.Events(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			var event session.Event
			payload := maps.Clone(call)
			if kind == "commit_pending" || kind == "commit_done" {
				payload = maps.Clone(commit)
			}
			if kind == "commit_done" {
				payload["pending_seq"], payload["pending_checksum"] = pending.Seq, pending.Checksum
				if strings.HasSuffix(scenario, "wrong closure") {
					payload["pending_checksum"] = "unrelated intent"
				}
			}
			if kind == "tool/dispatched" && strings.HasSuffix(scenario, "mismatched dispatch") {
				payload["arguments_json"] = `{"path":"different.go"}`
			}
			if kind == "tool/unknown" && strings.HasSuffix(scenario, "unconfirmed runtime") {
				payload["runtime_id"] = "unconfirmed-runtime"
			}
			if kind == "tool/call" {
				event, err = ledger.AppendSurface(ctx, created.ID, int64(len(events)), kind, payload, session.SurfaceOperation{Op: "append"})
			} else {
				event, err = ledger.Append(ctx, created.ID, int64(len(events)), kind, payload)
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "commit_pending" {
				pending = event
			}
			if kind == "tool/dispatched" && strings.HasSuffix(scenario, "duplicate dispatch") {
				if _, err := ledger.Append(ctx, created.ID, event.Seq+1, kind, payload); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if scenario == "missing tool result" || scenario == "unconfirmed tool stop" {
		appendFact := func(kind string, payload any) {
			t.Helper()
			events, err := ledger.Events(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Append(ctx, created.ID, int64(len(events)), kind, payload); err != nil {
				t.Fatal(err)
			}
		}
		appendFact("tool/dispatched", map[string]any{"run_id": run.RunID, "tool_call_id": "pending-shell", "tool_name": "Bash"})
		if scenario == "unconfirmed tool stop" {
			appendFact("tool/result", map[string]any{"run_id": run.RunID, "tool_call_id": "pending-shell", "tool_name": "Bash", "runtime_id": "unconfirmed-runtime", "outcome_unknown": true})
		}
	}
	if scenario != "missing cancel" {
		if _, err := second.CancelRun(ctx, session.CancelRunCommand{RequestID: "cancel", SessionID: created.ID, RunID: run.RunID, OwnerID: 7, Actor: actor}); err != nil {
			t.Fatal(err)
		}
		waitView(func(run session.RunView) bool {
			return run.Status == session.RunCanceling && run.CancellationState == "unknown"
		})
		if err := second.Close(); err != nil {
			t.Fatal(err)
		}
	}
	budget := admission.NewBudget(ledger)
	owner := admission.ActorOwner(actor)
	before, err := budget.Reserve(ctx, owner, created.ID, "retained-unknown", 103)
	if err != nil {
		t.Fatal(err)
	}
	if scenario != "active call" && !strings.HasSuffix(scenario, ": active call") {
		before, err = budget.MarkUnknown(ctx, owner, "retained-unknown")
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if scenario == "live scope" || strings.HasSuffix(scenario, ": live scope") {
		other := admission.NewGateway(budget, admission.ProviderConfig{})
		if err := other.Start(); err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		_, release, err := other.BindActor(actor, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	if err := gateway.FinishTask(ctx, actor); err == nil {
		t.Fatalf("ordinary finish accepted unconfirmed cleanup: %v", err)
	}
	beforeFacts, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
	if err != nil {
		t.Fatal(err)
	}
	requestActor := actor
	if scenario == "foreign actor" {
		requestActor.Subject = "foreign actor"
	}
	err = gateway.AbandonCanceledTask(ctx, requestActor)
	if scenario != "canceled" && scenario != "missing file read result" {
		if err == nil {
			t.Fatal("unfenced work or an unconfirmed source retired admission")
		}
		afterFacts, readErr := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
		if readErr != nil || len(afterFacts) != len(beforeFacts) {
			t.Fatal("rejected retirement changed admission facts")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	finishedFacts, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
	if err != nil || len(finishedFacts) != len(beforeFacts)+1 || finishedFacts[len(finishedFacts)-1].Type != "admission/task_finished" {
		t.Fatal("retirement did not append exactly one admission closure")
	}
	var closure struct{ Purpose string }
	if json.Unmarshal(finishedFacts[len(finishedFacts)-1].Payload, &closure) != nil || closure.Purpose != "abandoned" {
		t.Fatal("retirement was not distinguished from ordinary completion")
	}
	after, err := budget.Inspect(ctx, owner)
	if err != nil || after.ID != before.ID || after.TaskID != "" || after.UsedTokens != before.UsedTokens || after.ReservedTokens != before.ReservedTokens || !after.UnknownUsage || after.ActiveCalls != 0 || after.CostStatus != "unknown" {
		t.Fatalf("admission retirement changed accounting: %+v %v", after, err)
	}
	retained, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil || len(retained.Events) != len(snapshot.Events) {
		t.Fatal("admission retirement changed Session facts")
	}
	for i := range snapshot.Events {
		if retained.Events[i].Checksum != snapshot.Events[i].Checksum {
			t.Fatal("admission retirement rewrote the original Session")
		}
	}
	if err := session.ModelTaskIdle(ctx, ledger, actor); !errors.Is(err, session.ErrSessionStateConflict) {
		t.Fatal("admission retirement falsely confirmed Session cleanup")
	}
	if _, release, err := gateway.BindActor(actor, created.ID); !errors.Is(err, admission.ErrTaskClosed) {
		if release != nil {
			release()
		}
		t.Fatalf("retired identity admitted another model scope: %v", err)
	}
	fresh, err := workbench.Create(ctx, 7, "fixture", "independent task", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Reserve(ctx, owner, fresh.ID, "independent-call", 1); err != nil {
		t.Fatalf("retained cancellation blocked independent admission: %v", err)
	}
	if err := gateway.AbandonCanceledTask(ctx, actor); err != nil {
		t.Fatalf("retirement replay was not idempotent: %v", err)
	}
	current, err := budget.Inspect(ctx, owner)
	if err != nil || current.TaskID != fresh.ID || current.ActiveCalls != 1 {
		t.Fatal("retirement replay changed the independent task")
	}
}
