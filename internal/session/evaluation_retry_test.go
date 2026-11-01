package session_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/session"
)

type retryExternalDeletionLog struct {
	session.EventLog
	deleted atomic.Bool
}

func (l *retryExternalDeletionLog) AppendSurface(ctx context.Context, id string, seq int64, kind string, payload any, op session.SurfaceOperation) (session.Event, error) {
	event, err := l.EventLog.AppendSurface(ctx, id, seq, kind, payload, op)
	if err == nil && kind == "tool/call" && l.deleted.CompareAndSwap(false, true) {
		// An independently opened Workbench models another process's deletion:
		// its in-memory notification cannot replace the current Ledger check.
		err = session.NewWorkbench(l.EventLog, nil).Delete(ctx, 7, id, event.Seq+1)
	}
	return event, err
}

type retryDeletionConversation struct{ finished chan struct{} }

func (f retryDeletionConversation) RunConversation(ctx context.Context, _ orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	defer close(f.finished)
	result := handlers.Tool(ctx, orchestrator.ToolCall{ID: "delete-before-admission", Name: "Write", ParametersJSON: `{"path":"candidate.txt","content":"must not execute"}`})
	return orchestrator.ConversationResult{Success: result.Error == "", Message: "The fixture stopped at deletion."}, nil
}

func TestEvaluationRetryNeverAppendsPendingAfterExternalDeletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	base, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "fixture.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	ledger := &retryExternalDeletionLog{EventLog: base}
	workbench := session.NewWorkbench(ledger, nil)
	view, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "delete during tool admission", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := identity.Default().BindSession(view.ID)
	if err := session.BindEvaluation(ctx, ledger, view.ID, session.EvaluationBinding{SchemaVersion: 1, RunID: "delete-admission", TaskID: "delete-admission", Workspace: view.WorkingDir,
		Policy:  session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", ExecutionStrategy: "verify_retry", MaxToolRounds: 4, ContextMaxBytes: 12000},
		Runtime: session.EvaluationRuntime{VisibleVerificationCommands: []string{"go test ./... -count=1"}}}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	var effects atomic.Int64
	runner := session.NewSessionRunner(workbench, retryDeletionConversation{finished}, session.ToolExecutionFunc(func(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult {
		effects.Add(1)
		return orchestrator.ToolResult{}
	}), session.SessionRunnerOptions{Permissions: permission.NewController(map[string]permission.Level{"Write": permission.AutoAllow}, nil)})
	defer runner.Close()
	if _, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "delete-admission-turn", SessionID: view.ID, OwnerID: 7, ExpectedSeq: 2, Content: "Create the candidate", Actor: actor}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("public runner did not reach the external deletion boundary")
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := session.ReadVerifiedSnapshot(context.Background(), base, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	deleted := int64(-1)
	for _, event := range snapshot.Events {
		if event.Type == "session/deleted" {
			deleted = event.Seq
		}
		if deleted >= 0 && event.Seq > deleted {
			t.Fatalf("a fact was appended after the canonical tombstone: %s at %d", event.Type, event.Seq)
		}
	}
	if deleted < 0 || effects.Load() != 0 {
		t.Fatal("the fixture did not preserve deletion before any external effect")
	}
}
