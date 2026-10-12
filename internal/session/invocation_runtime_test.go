package session_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/session"
)

func TestInvocationRetainsUnknownRuntimeAcrossLedgerReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invocation.sqlite")
	manager := session.NewManager(session.NewSQLiteEventStore(path))
	defer manager.Close()
	current := manager.NewSession(t.TempDir())
	prepared, err := manager.BeginInvocation("call-1", "Bash", `{"command":"go test ./..."}`)
	if err != nil || len(prepared.OperationID) != 64 {
		t.Fatalf("remote effect has no durable operation: %+v %v", prepared, err)
	}
	if _, err := manager.MarkInvocationDispatched("call-1"); err != nil {
		t.Fatal(err)
	}
	runtimeID := prepared.OperationID + "/a10e5865-8f00-473e-8c20-5495b9f307f1/" + strings.Repeat("b", 64)
	retained, err := manager.CommitInvocation(session.InvocationResult{ID: "call-1", Name: "Bash", Output: "partial output", Error: "execution unknown", ExitCode: 126, Truncated: true, RuntimeID: runtimeID, Stopped: true, OutcomeUnknown: true})
	if err != nil || retained.Status != session.InvocationUnknown {
		t.Fatalf("partial result became a completed execution: %+v %v", retained, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := session.NewManager(session.NewSQLiteEventStore(path))
	defer reopened.Close()
	if _, err := reopened.Resume(context.Background(), current.ID); err != nil {
		t.Fatal(err)
	}
	again, err := reopened.BeginInvocation("call-1", "Bash", `{"command":"go test ./..."}`)
	if err != nil || again.Status != session.InvocationUnknown || again.OperationID != prepared.OperationID || again.RuntimeID != runtimeID || !again.Stopped || !again.OutcomeUnknown || !again.Truncated || again.Output != "partial output" {
		t.Fatalf("reopen lost the unknown resource identity: %+v %v", again, err)
	}
	if _, err := reopened.MarkInvocationDispatched("call-1"); err == nil {
		t.Fatal("unknown invocation was silently redispatched")
	}
	if _, err := reopened.ReconcileInvocation(session.InvocationResult{ID: "call-1", Name: "Bash", RuntimeID: "replacement-runtime", Stopped: true}); err == nil {
		t.Fatal("reconciliation replaced the retained physical identity")
	}
	if _, err := reopened.ReconcileInvocation(session.InvocationResult{ID: "call-1", Name: "Bash", RuntimeID: runtimeID, OutcomeUnknown: true}); err == nil {
		t.Fatal("unknown evidence was accepted as a reconciled outcome")
	}
	for _, result := range []session.InvocationResult{
		{ID: "call-1", Name: "Bash"},
		{ID: "call-1", Name: "Bash", RuntimeID: runtimeID},
	} {
		if _, err := reopened.ReconcileInvocation(result); err == nil {
			t.Fatal("missing identity or stop confirmation cleared a retained remote unknown")
		}
	}
	if record, err := reopened.ReconcileInvocation(session.InvocationResult{ID: "call-1", RuntimeID: runtimeID, Stopped: true, Output: "confirmed failure", ExitCode: 7}); err != nil || record.Status != session.InvocationCommitted || record.OutcomeUnknown || record.RuntimeID != runtimeID || !record.Stopped {
		t.Fatalf("explicit exact confirmation could not reconcile the retained runtime: %+v %v", record, err)
	}
}

func TestInvocationReconciliationKeepsUnconfirmedRuntimeHistory(t *testing.T) {
	manager := session.NewManager(session.NewMemoryStore())
	defer manager.Close()
	manager.NewSession(t.TempDir())
	intent, err := manager.BeginInvocation("call", "Bash", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.MarkInvocationDispatched(intent.ID); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []session.InvocationRuntime{
		{Phase: "pre_tool", RuntimeID: "retained-hook"},
		{Phase: "tool", RuntimeID: "retained-tool"},
	} {
		if err := manager.RecordInvocationRuntime(intent.ID, receipt.Phase, receipt.RuntimeID, receipt.Stopped); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.CommitInvocation(session.InvocationResult{ID: intent.ID, Name: "Bash", RuntimeID: "retained-tool", OutcomeUnknown: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReconcileInvocation(session.InvocationResult{ID: intent.ID, RuntimeID: "retained-tool", Stopped: true}); err == nil {
		t.Fatal("confirming one runtime cleared an invocation with another unconfirmed runtime")
	}
	record := manager.Current().Invocations[0]
	if record.Status != session.InvocationUnknown || !record.OutcomeUnknown || len(record.Runtimes) != 2 || record.Runtimes[0].Stopped || record.Runtimes[1].Stopped {
		t.Fatalf("rejected reconciliation changed retained runtime facts: %+v", record)
	}
	result := session.InvocationResult{ID: intent.ID, RuntimeID: "retained-tool", Stopped: true, Runtimes: []session.InvocationRuntime{
		{Phase: "pre_tool", RuntimeID: "replacement-hook", Stopped: true},
		{Phase: "tool", RuntimeID: "retained-tool", Stopped: true},
	}}
	if _, err := manager.ReconcileInvocation(result); err == nil {
		t.Fatal("replacement Hook identity was accepted as stop confirmation")
	}
	result.Runtimes[0].RuntimeID = "retained-hook"
	reconciled, err := manager.ReconcileInvocation(result)
	if err != nil || reconciled.Status != session.InvocationCommitted || reconciled.OutcomeUnknown || !reconciled.Runtimes[0].Stopped || !reconciled.Runtimes[1].Stopped {
		t.Fatalf("explicit confirmation of every exact runtime was not retained: %+v %v", reconciled, err)
	}
}

func TestInvocationKeepsEachRuntimeBeforeTheNextEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-history.sqlite")
	manager := session.NewManager(session.NewSQLiteEventStore(path))
	defer manager.Close()
	current := manager.NewSession(t.TempDir())
	intent, err := manager.BeginInvocation("call", "Bash", `{"command":"go test ./..."}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.MarkInvocationDispatched(intent.ID); err != nil {
		t.Fatal(err)
	}
	first := intent.OperationID + "/a10e5865-8f00-473e-8c20-5495b9f307f1/" + strings.Repeat("b", 64)
	if err := manager.RecordInvocationRuntime(intent.ID, "pre_tool", first, true); err != nil {
		t.Fatal(err)
	}
	second := intent.OperationID + "/b10e5865-8f00-473e-8c20-5495b9f307f1/" + strings.Repeat("c", 64)
	if err := manager.RecordInvocationRuntime(intent.ID, "tool", second, true); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := session.NewManager(session.NewSQLiteEventStore(path))
	defer reopened.Close()
	restored, err := reopened.Resume(context.Background(), current.ID)
	if err != nil {
		t.Fatal(err)
	}
	record := restored.Invocations[0]
	if record.Status != session.InvocationDispatched || record.Stopped || len(record.Runtimes) != 2 || record.Runtimes[0].RuntimeID != first || !record.Runtimes[0].Stopped || record.Runtimes[1].RuntimeID != second || record.Runtimes[1].Phase != "tool" {
		t.Fatalf("crash lost an earlier runtime or confused it with final completion: %+v", record)
	}
	if err := reopened.RecordInvocationRuntime(intent.ID, "tool", second, false); err == nil {
		t.Fatal("duplicate runtime changed its confirmed stop identity")
	}
}

func TestInvocationSurvivesForkAndRewindBeforeResultCommit(t *testing.T) {
	manager := session.NewManager(session.NewSQLiteEventStore(filepath.Join(t.TempDir(), "history.sqlite")))
	defer manager.Close()
	manager.NewSession(t.TempDir())
	manager.Append(session.RoleUser, "keep")
	completed, err := manager.BeginInvocation("completed", "Bash", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.MarkInvocationDispatched(completed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CommitInvocation(session.InvocationResult{ID: completed.ID, Name: completed.Name, Output: "cached output"}); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"fork", "rewind"} {
		intent, err := manager.BeginInvocation(operation, "SessionControl", `{}`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.MarkInvocationDispatched(intent.ID); err != nil {
			t.Fatal(err)
		}
		if operation == "fork" {
			_, err = manager.Fork(context.Background(), "child", 1)
		} else {
			_, err = manager.Rewind(context.Background(), 1)
		}
		if err != nil {
			t.Fatal(err)
		}
		cached, err := manager.BeginInvocation(completed.ID, completed.Name, completed.ArgumentsJSON)
		if err != nil || cached.Status != session.InvocationCommitted || cached.OperationID != completed.OperationID || cached.Output != "cached output" {
			t.Fatalf("%s made a committed invocation executable again: %+v %v", operation, cached, err)
		}
		restored, err := manager.BeginInvocation(intent.ID, intent.Name, intent.ArgumentsJSON)
		if err != nil || restored.OperationID != intent.OperationID || restored.Status != session.InvocationDispatched {
			t.Fatalf("%s lost a durable in-flight invocation: %+v %v", operation, restored, err)
		}
		if _, err := manager.CommitInvocation(session.InvocationResult{ID: intent.ID, Name: intent.Name, Output: "history changed"}); err != nil {
			t.Fatalf("%s could not persist its result: %v", operation, err)
		}
	}
}

func TestInvocationHistoryRestoresCurrentPairedResult(t *testing.T) {
	manager := session.NewManager(session.NewSQLiteEventStore(filepath.Join(t.TempDir(), "paired-history.sqlite")))
	defer manager.Close()
	manager.NewSession(t.TempDir())
	intent, err := manager.BeginInvocation("call", "Bash", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.MarkInvocationDispatched(intent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CommitInvocation(session.InvocationResult{ID: intent.ID, Name: intent.Name, Output: "partial output", Error: "unknown", OutcomeUnknown: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReconcileInvocation(session.InvocationResult{ID: intent.ID, Name: intent.Name, Output: "confirmed output"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Rewind(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	messages := manager.Messages()
	if last := messages[len(messages)-1]; last.ToolCallID != intent.ID || last.Content != "confirmed output" || last.IsError {
		t.Fatalf("rewind paired a confirmed invocation with its stale unknown result: %+v", messages)
	}
	manager.Append(session.RoleUser, "keep recent")
	manager.ReplaceMessages("compacted", 1)
	if _, err := manager.Fork(context.Background(), "compacted-child", 8); err != nil {
		t.Fatal(err)
	}
	var callSeen bool
	for _, message := range manager.Messages() {
		for _, call := range message.ToolCalls {
			callSeen = callSeen || call.ID == intent.ID
		}
		if message.Role == session.RoleTool && message.ToolCallID == intent.ID && !callSeen {
			t.Fatal("compacted history produced a result without its retained call")
		}
	}
	if !callSeen {
		t.Fatal("compacted history lost its retained invocation message")
	}
}
