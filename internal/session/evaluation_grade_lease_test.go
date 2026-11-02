package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/safety"
	"code-agent/internal/sandbox"
)

type leaseGradeRuntime struct{ calls int }

// Only the existing fixed checker executes locally; this is not OpenSandbox evidence.
func (r *leaseGradeRuntime) Run(ctx context.Context, request sandbox.Request) (sandbox.Result, error) {
	r.calls++
	command := exec.CommandContext(ctx, "go", request.Args...)
	command.Dir, command.Env = request.Workspace, safety.ScrubEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	code := -1
	if command.ProcessState != nil {
		code = command.ProcessState.ExitCode()
	}
	return sandbox.Result{Output: string(output), ExitCode: code, Stopped: true, RuntimeID: request.OperationID + "/11111111-2222-4333-8444-555555555555/" + strings.Repeat("a", 64)}, err
}

func expiredGradeCandidate(t *testing.T, active bool, wrap ...func(EventLog) EventLog) (*TaskWorkspaces, EventLog, EvaluationGradeCommand, TaskWorkspaceView, *leaseGradeRuntime, string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first native task copy and file pin contract")
	}
	ctx := context.Background()
	repo, storage := taskWorkspaceRepository(t), t.TempDir()
	var ledger EventLog = openWorkbenchTestLedger(t)
	if len(wrap) > 0 {
		ledger = wrap[0](ledger)
	}
	w := NewWorkbench(ledger, nil)
	created, err := w.CreateWithWorkingDir(ctx, 7, "repo", "expired grading candidate", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	checker := &leaseGradeRuntime{}
	tasks := NewTaskWorkspaces(w, taskWorkspaceOwner, repo, storage).WithBaselineRuntime(TaskBaselineRuntime{
		Toolchain: "go", Runner: checker, Ready: func(context.Context) error { return nil },
		Acquire: func(context.Context, uint, string) error { return nil }, Finish: func(context.Context, uint, string) error { return nil },
		BeginProposal: func(context.Context, uint, string) (func() error, error) { return func() error { return nil }, nil },
	})
	tasks.manager.SetAgentLeaseTTL(10 * time.Second)
	prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare-grade-candidate")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("candidate preparation: %+v %v", prepared, err)
	}
	actual := filepath.Join(storage, prepared.WorkspaceID)
	binding := EvaluationBinding{SchemaVersion: 1, RunID: "grade-after-expiry", TaskID: "retained-candidate", Model: "fixture-only", RuntimeContract: "production_opensandbox", SourceWorkspace: repo, Workspace: actual, Lifecycle: "hold",
		Policy: EvaluationPolicy{ContextStrategy: "metadata_on_demand", ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000}, Runtime: EvaluationRuntime{TaskWorkspaceRoot: storage, OpenSandbox: &sandbox.OpenSandboxIdentity{}}}
	if err := BindEvaluation(ctx, ledger, created.ID, binding); err != nil {
		t.Fatal(err)
	}
	var adapter ConversationAdapter = &recordingConversationAdapter{reply: "candidate completed"}
	if active {
		adapter = &blockingConversationAdapter{started: make(chan struct{}), finished: make(chan struct{})}
	}
	runner := NewSessionRunner(w, adapter, nil, SessionRunnerOptions{WorkerID: "grade-candidate", Workspace: tasks.ModelWorkspace})
	t.Cleanup(func() { _ = runner.Close() })
	current, err := w.Get(ctx, 7, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runner.SubmitMessage(ctx, SubmitMessageCommand{RequestID: "candidate-message", SessionID: created.ID, OwnerID: 7, ExpectedSeq: int64(current.EventCount), Content: "acknowledge the candidate", Actor: testRunnerActor()})
	if err != nil {
		t.Fatal(err)
	}
	if active {
		waitForRunStatus(t, runner, created.ID, run.RunID, RunRunning)
	} else {
		waitForRunStatus(t, runner, created.ID, run.RunID, RunCompleted)
		_ = runner.Close()
	}
	prepared, err = tasks.Inspect(ctx, 7, created.ID)
	if err != nil || prepared.LeaseExpiresAt == nil {
		t.Fatal("candidate lease is unavailable")
	}
	remaining := time.Until(*prepared.LeaseExpiresAt)
	if remaining > 10*time.Second {
		t.Fatal("short fixture lease was unexpectedly renewed before expiry")
	}
	time.Sleep(remaining + 20*time.Millisecond)
	expired, err := tasks.Inspect(ctx, 7, created.ID)
	if err != nil || expired.State != "blocked" {
		t.Fatalf("fixture did not expire honestly: %+v %v", expired, err)
	}
	checks := []byte(`{"schema_version":1,"files":[{"path":"main.go","contains":"dirty"}]}`)
	path := filepath.Join(t.TempDir(), "operator-checks.json")
	if err := os.WriteFile(path, checks, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, ledger, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := testRunnerActor().BindSession(created.ID)
	command := EvaluationGradeCommand{OwnerID: 7, Actor: actor, SessionID: created.ID, RequestID: "grade-retained-candidate", ExpectedSessionHeadChecksum: snapshot.Events[len(snapshot.Events)-1].Checksum, ChecksPath: path, ChecksSHA256: hashCodeState(string(checks))}
	return tasks, ledger, command, prepared, checker, actual
}

func TestEvaluationGradeExpiredCandidateRequiresFreshCAS(t *testing.T) {
	tasks, ledger, command, prepared, checker, actual := expiredGradeCandidate(t, false)
	ctx := context.Background()
	before, _ := ReadVerifiedSnapshot(ctx, ledger, command.SessionID)
	originalScope := tasks.baselineRuntime.BeginProposal
	for _, name := range []string{"stale-head", "foreign-owner", "foreign-actor", "bad-checks", "missing-grader-scope"} {
		t.Run(name, func(t *testing.T) {
			invalid := command
			switch name {
			case "stale-head":
				invalid.ExpectedSessionHeadChecksum = strings.Repeat("0", 64)
			case "foreign-owner":
				invalid.OwnerID = 8
			case "foreign-actor":
				invalid.Actor.Subject = "another-subject"
			case "bad-checks":
				invalid.ChecksSHA256 = strings.Repeat("1", 64)
			case "missing-grader-scope":
				tasks.WithProposalScope(nil)
				t.Cleanup(func() { tasks.WithProposalScope(originalScope) })
			}
			if _, err := tasks.GradeEvaluation(ctx, invalid); err == nil {
				t.Fatal("invalid grade command was accepted")
			}
			after, _ := ReadVerifiedSnapshot(ctx, ledger, command.SessionID)
			if len(after.Events) != len(before.Events) || after.Events[len(after.Events)-1].Checksum != command.ExpectedSessionHeadChecksum || checker.calls != 0 {
				t.Fatal("invalid grade command renewed the lease or dispatched the checker")
			}
		})
	}
	grade, err := tasks.GradeEvaluation(ctx, command)
	if !errors.Is(err, ErrSequenceConflict) || grade.Reason != "grade_session_changed" || checker.calls != 0 {
		t.Fatalf("expired completed candidate did not require a fresh cursor: %+v %v", grade, err)
	}
	after, err := ReadVerifiedSnapshot(ctx, ledger, command.SessionID)
	if err != nil || len(after.Events) != len(before.Events)+1 {
		t.Fatal("controlled revalidation did not append exactly one fact")
	}
	renewal := after.Events[len(before.Events)]
	if renewal.Type != "workspace/task-lease-renewed" || renewal.PrevChecksum != command.ExpectedSessionHeadChecksum {
		t.Fatal("grade preflight appended something other than the retained lease renewal")
	}
	for index, event := range before.Events {
		if after.Events[index].EventID != event.EventID || after.Events[index].Checksum != event.Checksum {
			t.Fatal("lease revalidation rewrote prior candidate evidence")
		}
	}
	data, _ := json.Marshal(grade)
	var receipt map[string]any
	_ = json.Unmarshal(data, &receipt)
	proof, ok := receipt["lease_revalidation"].(map[string]any)
	if !ok || proof["before_head_checksum"] != command.ExpectedSessionHeadChecksum || proof["after_head_checksum"] != renewal.Checksum || proof["event_id"] != renewal.EventID || proof["event_checksum"] != renewal.Checksum || proof["lease_id"] != prepared.WorkspaceID || proof["verified"] != true {
		t.Fatal("controlled lease revalidation omitted its canonical cursor and event evidence")
	}
	expires, isTime := proof["expires_at"].(string)
	renewedUntil, parseErr := time.Parse(time.RFC3339Nano, expires)
	if !isTime || parseErr != nil || !renewedUntil.After(time.Now()) || proof["previous_expires_at"] != prepared.LeaseExpiresAt.UTC().Format(time.RFC3339Nano) {
		t.Fatal("lease revalidation omitted the original and renewed expiry")
	}
	command.ExpectedSessionHeadChecksum = renewal.Checksum
	grade, err = tasks.GradeEvaluation(ctx, command)
	if err != nil || grade.State != "completed" || grade.Passed == nil || !*grade.Passed || checker.calls != 1 {
		t.Fatalf("fresh-cursor grading did not reuse the completed candidate: %+v %v", grade, err)
	}
	graded, _ := ReadVerifiedSnapshot(ctx, ledger, command.SessionID)
	replayed, err := tasks.GradeEvaluation(ctx, command)
	replayedSnapshot, _ := ReadVerifiedSnapshot(ctx, ledger, command.SessionID)
	if err != nil || !replayed.AlreadyRecorded || checker.calls != 1 || len(replayedSnapshot.Events) != len(graded.Events) {
		t.Fatal("recorded grade was dispatched or renewed again")
	}
	if body, err := os.ReadFile(filepath.Join(actual, "main.go")); err != nil || string(body) != "dirty" {
		t.Fatal("grading changed the retained candidate")
	}
}

type gradeLeaseCASConflict struct {
	EventLog
	attempts int
}

func (l *gradeLeaseCASConflict) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == "workspace/task-lease-renewed" {
		l.attempts++
		if l.attempts == 1 {
			if _, err := NewWorkbench(l.EventLog, nil).AppendUserMessage(ctx, 7, id, seq, "concurrent user input"); err != nil {
				return Event{}, err
			}
		}
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestEvaluationGradeExpiredCandidateNeverRenewsAfterCASConflict(t *testing.T) {
	var conflict *gradeLeaseCASConflict
	tasks, ledger, command, _, checker, _ := expiredGradeCandidate(t, false, func(ledger EventLog) EventLog {
		conflict = &gradeLeaseCASConflict{EventLog: ledger}
		return conflict
	})
	before, _ := ReadVerifiedSnapshot(context.Background(), ledger, command.SessionID)
	grade, err := tasks.GradeEvaluation(context.Background(), command)
	if !errors.Is(err, ErrSequenceConflict) || grade.Reason != "grade_session_changed" || conflict.attempts != 1 || checker.calls != 0 {
		t.Fatalf("grade followed a newer cursor after a CAS conflict: %+v %v", grade, err)
	}
	after, _ := ReadVerifiedSnapshot(context.Background(), ledger, command.SessionID)
	if len(after.Events) != len(before.Events)+1 || after.Events[len(before.Events)].Type == "workspace/task-lease-renewed" {
		t.Fatal("stale grade command renewed the concurrently changed candidate")
	}
	encoded, _ := json.Marshal(grade)
	if strings.Contains(string(encoded), `"lease_revalidation"`) {
		t.Fatal("a foreign cursor change was reported as controlled lease revalidation")
	}
}

func TestEvaluationGradeExpiredCandidateRefusesOtherBlockedScopes(t *testing.T) {
	for _, reason := range []string{"active-run", "unrecorded-tree"} {
		t.Run(reason, func(t *testing.T) {
			tasks, ledger, command, _, checker, actual := expiredGradeCandidate(t, reason == "active-run")
			if reason == "unrecorded-tree" {
				if err := os.WriteFile(filepath.Join(actual, "main.go"), []byte("unrecorded mutation"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := ReadVerifiedSnapshot(context.Background(), ledger, command.SessionID)
			if _, err := tasks.GradeEvaluation(context.Background(), command); err == nil {
				t.Fatal("other blocked scope was silently admitted")
			}
			after, _ := ReadVerifiedSnapshot(context.Background(), ledger, command.SessionID)
			if len(after.Events) != len(before.Events) || checker.calls != 0 {
				t.Fatal("other blocked scope renewed or dispatched the checker")
			}
		})
	}
}
