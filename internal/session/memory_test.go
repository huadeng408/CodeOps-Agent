package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
)

type observedMemoryDeadline struct{ remaining chan time.Duration }

func (m *observedMemoryDeadline) Commit(ctx context.Context, _ string) error {
	deadline, _ := ctx.Deadline()
	m.remaining <- time.Until(deadline)
	return nil
}

func (*observedMemoryDeadline) Recall(context.Context, uint, string, int) (string, error) {
	return "", nil
}

func TestRunnerReflectionHonorsExplicitGoRequestDeadline(t *testing.T) {
	for _, profile := range []struct {
		name          string
		timeout, want time.Duration
	}{{"default", 0, 30 * time.Second}, {"explicit", 180 * time.Second, 180 * time.Second}} {
		t.Run(profile.name, func(t *testing.T) {
			ctx := context.Background()
			workbench := NewWorkbench(openWorkbenchTestLedger(t), nil)
			created, err := workbench.Create(ctx, 7, "repo", "reflection deadline", "")
			if err != nil {
				t.Fatal(err)
			}
			memory := &observedMemoryDeadline{remaining: make(chan time.Duration, 1)}
			runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "Completed"}, nil, SessionRunnerOptions{Memory: memory, MemoryCommitTimeout: profile.timeout})
			defer runner.Close()
			run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "deadline", SessionID: created.ID, OwnerID: 7, ExpectedSeq: 1, Content: "Complete", Actor: identity.Default()})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case remaining := <-memory.remaining:
				if remaining < profile.want-time.Second || remaining > profile.want {
					t.Fatalf("reflection deadline = %s, want %s", remaining, profile.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("reflection did not receive its Go-controlled deadline")
			}
			waitForRunStatus(t, runner, created.ID, run.RunID, RunCompleted)
		})
	}
}

type canceledCommitMemory struct{}

func (canceledCommitMemory) Commit(ctx context.Context, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (canceledCommitMemory) Recall(context.Context, uint, string, int) (string, error) {
	panic("recall must not run during memory commit")
}

type scopedRecallMemory struct{ query MemoryQuery }

func (*scopedRecallMemory) Commit(context.Context, string) error { return nil }
func (*scopedRecallMemory) Recall(context.Context, uint, string, int) (string, error) {
	return `{"entries":[]}`, nil
}
func (m *scopedRecallMemory) RecallWithOptions(_ context.Context, _ uint, query MemoryQuery) (string, error) {
	m.query = query
	return `{"entries":[]}`, nil
}
func (*scopedRecallMemory) Manage(context.Context, string, MemoryCommand) (string, error) {
	return "", nil
}

func TestMemoryCommitTimeoutRetainsSuccessfulTerminalAndBlockedReceipt(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	workbench := NewWorkbench(ledger, nil)
	view, err := workbench.Create(ctx, 7, "repo", "fixture memory cancellation", "")
	if err != nil {
		t.Fatal(err)
	}
	runner := NewSessionRunner(workbench, &recordingConversationAdapter{reply: "Completed"}, nil, SessionRunnerOptions{
		Memory: canceledCommitMemory{}, MemoryCommitTimeout: 20 * time.Millisecond,
	})
	defer runner.Close()
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "memory-timeout", SessionID: view.ID, OwnerID: 7,
		ExpectedSeq: int64(view.EventCount), Content: "Complete", Actor: testRunnerActor()})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitForRunStatus(t, runner, view.ID, run.RunID, RunCompleted)
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	terminal, err := runner.Run(ctx, view.ID, run.RunID)
	if err != nil || terminal.Status != RunCompleted {
		t.Fatalf("Memory failure changed the successful task: terminal=%+v err=%v", terminal, err)
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, ledger, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		RunID     string `json:"run_id"`
		ErrorCode string `json:"error_code"`
		Retryable bool   `json:"retryable"`
	}
	blocked := 0
	for _, event := range snapshot.Events {
		if event.Type == "memory/commit-blocked" {
			blocked++
			if err := json.Unmarshal(event.Payload, &receipt); err != nil {
				t.Fatal(err)
			}
		}
	}
	if blocked != 1 || receipt.RunID != run.RunID || receipt.ErrorCode != "memory_commit_unavailable" || !receipt.Retryable {
		t.Fatal("memory cancellation receipt must retain the fixed, retryable category")
	}
}

func TestIndependentAgentRecallIsScopedToChildSession(t *testing.T) {
	runner, parent, actor := agentTestRunner(t, &independentAgentFixture{})
	task := agentTestCall(t, runner, actor, "memory-scope", "SpawnAgent", `{"kind":"explore","title":"recall","objective":"recall only child history"}`)
	memory := &scopedRecallMemory{}
	runner.options.Memory = memory

	childEvents, err := runner.workbench.ledger.Events(context.Background(), task.ChildSessionId)
	if err != nil {
		t.Fatal(err)
	}
	result := runner.recallSessionMemory(context.Background(), childEvents, orchestrator.ToolCall{
		ID: "child-recall", Name: "RecallMemory", ParametersJSON: `{"query":"history"}`,
	})
	if result.ExitCode != 0 || memory.query.SourceSessionID != task.ChildSessionId {
		t.Fatalf("child recall scope = %q, result=%+v", memory.query.SourceSessionID, result)
	}

	parentEvents, err := runner.workbench.ledger.Events(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	result = runner.recallSessionMemory(context.Background(), parentEvents, orchestrator.ToolCall{
		ID: "parent-recall", Name: "RecallMemory", ParametersJSON: `{"query":"history"}`,
	})
	if result.ExitCode != 0 || memory.query.SourceSessionID != "" {
		t.Fatalf("parent recall unexpectedly scoped = %q, result=%+v", memory.query.SourceSessionID, result)
	}
}
