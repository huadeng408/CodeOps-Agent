package session_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"code-agent/internal/identity"
	"code-agent/internal/safety"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
	"code-agent/internal/tools"
)

// This local protocol fixture runs only the fixed trusted checker. It does not
// establish OpenSandbox, provider, or product acceptance.
type graderRuntimeFixture struct {
	gradeCalls int
	gradeDir   string
	unknown    bool
	outcome    string
	closed     int
}

func (f *graderRuntimeFixture) Run(ctx context.Context, request sandbox.Request) (sandbox.Result, error) {
	if request.FrozenInput != nil {
		return sandbox.Result{ExitCode: 0, Stopped: true, InputSHA256: request.FrozenInput.SourceSHA256, RuntimeID: "fixture-proposal"}, nil
	}
	f.gradeCalls++
	f.gradeDir = request.Workspace
	if f.outcome != "" {
		result := sandbox.Result{ExitCode: 126, Stopped: true, RuntimeID: request.OperationID + "/11111111-2222-4333-8444-555555555555/" + strings.Repeat("a", 64)}
		switch f.outcome {
		case "unavailable":
			return result, fmt.Errorf("%w: RUNTIME_PRIVATE_ERROR", sandbox.ErrSandboxUnavailable)
		case "stopped-unknown":
			return result, fmt.Errorf("%w: RUNTIME_PRIVATE_ERROR", sandbox.ErrSandboxOutcomeUnknown)
		case "nonzero":
			result.ExitCode = 7
		case "parser-bad":
			result.ExitCode, result.Output = 0, "RUNTIME_PRIVATE_OUTPUT"
		}
		return result, nil
	}
	if f.unknown {
		return sandbox.Result{ExitCode: 126, RuntimeID: request.OperationID + "/11111111-2222-4333-8444-555555555555/" + strings.Repeat("a", 64)}, sandbox.ErrSandboxOutcomeUnknown
	}
	if request.Program != "go" || strings.Join(request.Args, " ") != "run grader.go" || request.Command != "" {
		return sandbox.Result{}, fmt.Errorf("unexpected fixture request")
	}
	command := exec.CommandContext(ctx, "go", request.Args...)
	command.Dir = request.Workspace
	command.Env = safety.ScrubEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	return sandbox.Result{Output: string(output), ExitCode: command.ProcessState.ExitCode(), Stopped: true, RuntimeID: request.OperationID + "/11111111-2222-4333-8444-555555555555/" + strings.Repeat("a", 64)}, err
}

type graderFixture struct {
	tasks    *session.TaskWorkspaces
	ledger   session.EventLog
	actor    identity.Actor
	id       string
	actual   string
	storage  string
	runtime  *graderRuntimeFixture
	proposal session.TaskWorkspaceView
}

func prepareGraderFixture(t *testing.T, wrap ...func(session.EventLog) session.EventLog) graderFixture {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("production task preparation uses admitted Windows file pins")
	}
	ctx := context.Background()
	repo, storage := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "public.txt"), []byte("public task input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture"}} {
		command := exec.Command("git", append([]string{"-C", repo}, args...)...)
		command.Env = safety.ScrubGitEnvironment(os.Environ())
		if body, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, body)
		}
	}
	baseLedger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "fixture.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = baseLedger.Close() })
	var ledger session.EventLog = baseLedger
	if len(wrap) > 0 {
		ledger = wrap[0](ledger)
	}
	w := session.NewWorkbench(ledger, nil)
	created, err := w.CreateWithWorkingDir(ctx, 7, "fixture", "hidden grader", "public task", repo)
	if err != nil {
		t.Fatal(err)
	}
	f := &graderRuntimeFixture{}
	tasks := session.NewTaskWorkspaces(w, func(context.Context) (uint, error) { return 7, nil }, repo, storage).WithBaselineRuntime(session.TaskBaselineRuntime{
		Toolchain: "go", Runner: f, Ready: func(context.Context) error { return nil },
		Acquire: func(context.Context, uint, string) error { return nil }, Finish: func(context.Context, uint, string) error { return nil },
		BeginProposal: func(context.Context, uint, string) (func() error, error) {
			return func() error { f.closed++; return nil }, nil
		},
	})
	prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "fixture-prepare")
	if err != nil || prepared.State != "prepared" {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	actual := filepath.Join(storage, prepared.WorkspaceID)
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding := session.EvaluationBinding{SchemaVersion: 1, RunID: "grader-fixture", TaskID: "hidden-check", Model: "fixture-only", RuntimeContract: "production_opensandbox", SourceWorkspace: repo, Workspace: actual, Lifecycle: "hold",
		Policy:  session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000},
		Runtime: session.EvaluationRuntime{TaskWorkspaceRoot: storage, OpenSandbox: &sandbox.OpenSandboxIdentity{}}}
	if err := session.BindEvaluation(ctx, ledger, created.ID, binding); err != nil {
		t.Fatal(err)
	}
	executor := tools.NewExecutor(actual)
	t.Cleanup(func() { _ = executor.Close() })
	request := tools.ToolRequest{Name: "Write", Arguments: map[string]any{"path": "answer.json", "content": `{"label":"visible-output","flag":true,"number":1}`}}
	appendFact := func(kind string, payload any) {
		events, err := ledger.Events(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(ctx, created.ID, int64(len(events)), kind, payload); err != nil {
			t.Fatal(err)
		}
	}
	appendFact("tool/dispatched", map[string]any{"run_id": "fixture-run", "tool_call_id": "write", "tool_name": "Write"})
	result, err := executor.Execute(ctx, request)
	if err != nil || len(result.Changes) != 1 {
		t.Fatalf("fixture Go Write failed: %v", err)
	}
	change := result.Changes[0]
	appendFact("code/modified", map[string]any{"run_id": "fixture-run", "tool_call_id": "write", "tool_name": "Write", "operation": "Write", "summary": "fixture effect", "path": change.Path, "before": change.Before, "after": change.After, "before_exists": *change.BeforeExists, "after_exists": *change.AfterExists, "before_sha256": graderHash([]byte(change.Before)), "after_sha256": graderHash([]byte(change.After)), "diff_sha256": graderHash([]byte(change.Before + "\x00" + change.After))})
	appendFact("tool/result", map[string]any{"run_id": "fixture-run", "tool_call_id": "write", "tool_name": "Write", "exit_code": 0})
	events, _ := ledger.Events(ctx, created.ID)
	proposal, err := tasks.GenerateProposal(ctx, 7, created.ID, int64(len(events)), "fixture-proposal")
	if err != nil || proposal.Proposal == nil || proposal.Proposal.State != "ready" {
		t.Fatalf("proposal: %+v %v", proposal, err)
	}
	return graderFixture{tasks: tasks, ledger: ledger, actor: actor, id: created.ID, actual: actual, storage: storage, runtime: f, proposal: proposal}
}

type lostGradeResult struct{ session.EventLog }

func (l lostGradeResult) Append(ctx context.Context, id string, seq int64, kind string, payload any) (session.Event, error) {
	if kind == "evaluation/grade-result" {
		return session.Event{}, errors.New("fixture grade result append unavailable")
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

type foreignGradeRuntime struct{ session.EventLog }

func (l foreignGradeRuntime) Append(ctx context.Context, id string, seq int64, kind string, payload any) (session.Event, error) {
	if kind == "evaluation/grade-result" {
		data, _ := json.Marshal(payload)
		var value map[string]any
		_ = json.Unmarshal(data, &value)
		view := value["view"].(map[string]any)
		view["runtime_id"] = strings.Repeat("f", 64) + "/11111111-2222-4333-8444-555555555555/" + strings.Repeat("a", 64)
		payload = value
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestEvaluationGradeRejectsForeignRuntimeStopProof(t *testing.T) {
	f := prepareGraderFixture(t, func(log session.EventLog) session.EventLog { return foreignGradeRuntime{log} })
	ctx := context.Background()
	checks := []byte(`{"schema_version":1,"files":[{"path":"answer.json","contains":"visible-output"}]}`)
	path := filepath.Join(t.TempDir(), "checks.json")
	if err := os.WriteFile(path, checks, 0600); err != nil {
		t.Fatal(err)
	}
	events, _ := f.ledger.Events(ctx, f.id)
	_, _ = f.tasks.GradeEvaluation(ctx, session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "foreign-runtime", ExpectedSessionHeadChecksum: events[len(events)-1].Checksum, ChecksPath: path, ChecksSHA256: graderHash(checks)})
	events, _ = f.ledger.Events(ctx, f.id)
	if _, err := session.ReadEvaluationGrade(events); !errors.Is(err, session.ErrEventIntegrity) {
		t.Fatal("a different remote operation was accepted as the retained grader stop proof")
	}
}

func TestEvaluationGradePreflightReportsSessionChangeWithoutUnknownEffect(t *testing.T) {
	f := prepareGraderFixture(t)
	checks := []byte(`{"schema_version":1,"files":[{"path":"answer.json","contains":"visible-output"}]}`)
	path := filepath.Join(t.TempDir(), "checks.json")
	if err := os.WriteFile(path, checks, 0600); err != nil {
		t.Fatal(err)
	}
	events, _ := f.ledger.Events(context.Background(), f.id)
	grade, err := f.tasks.GradeEvaluation(context.Background(), session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "stale-source-head", ExpectedSessionHeadChecksum: strings.Repeat("0", 64), ChecksPath: path, ChecksSHA256: graderHash(checks)})
	if err == nil || grade.State != "blocked" || grade.Reason != "grade_session_changed" || grade.IntentEventID != "" || grade.RuntimeID != "" || grade.OperationID != "" || grade.Passed != nil || f.runtime.gradeCalls != 0 {
		t.Fatalf("pre-dispatch validation invented an unknown remote effect: %+v %v", grade, err)
	}
	after, _ := f.ledger.Events(context.Background(), f.id)
	if len(after) != len(events) || after[len(after)-1].Checksum != events[len(events)-1].Checksum {
		t.Fatal("preflight failure changed the canonical facts")
	}
}

func TestEvaluationGradePreflightRejectsDifferentApprovedRoot(t *testing.T) {
	f := prepareGraderFixture(t)
	ctx := context.Background()
	workbench := session.NewWorkbench(f.ledger, nil)
	view, err := workbench.Get(ctx, 7, f.id)
	if err != nil {
		t.Fatal(err)
	}
	other := session.NewTaskWorkspaces(workbench, func(context.Context) (uint, error) { return 7, nil }, view.WorkingDir, filepath.Join(f.storage, "another-root"))
	events, _ := f.ledger.Events(ctx, f.id)
	grade, err := other.GradeEvaluation(ctx, session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "different-root", ExpectedSessionHeadChecksum: events[len(events)-1].Checksum, ChecksSHA256: strings.Repeat("a", 64)})
	if err == nil || grade.State != "blocked" || grade.Reason != "grade_binding_mismatch" || grade.IntentEventID != "" || grade.RuntimeID != "" || f.runtime.gradeCalls != 0 {
		t.Fatalf("path normalization admitted a different approved root: %+v %v", grade, err)
	}
	after, _ := f.ledger.Events(ctx, f.id)
	if len(after) != len(events) || after[len(after)-1].Checksum != events[len(events)-1].Checksum {
		t.Fatal("foreign-root preflight changed the canonical facts")
	}
}

func TestEvaluationGradePendingIntentCannotReplayOrReleaseTask(t *testing.T) {
	f := prepareGraderFixture(t, func(log session.EventLog) session.EventLog { return lostGradeResult{log} })
	ctx := context.Background()
	checks := []byte(`{"schema_version":1,"files":[{"path":"answer.json","contains":"visible-output"}]}`)
	path := filepath.Join(t.TempDir(), "checks.json")
	if err := os.WriteFile(path, checks, 0600); err != nil {
		t.Fatal(err)
	}
	events, _ := f.ledger.Events(ctx, f.id)
	command := session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "lost-result", ExpectedSessionHeadChecksum: events[len(events)-1].Checksum, ChecksPath: path, ChecksSHA256: graderHash(checks)}
	grade, err := f.tasks.GradeEvaluation(ctx, command)
	if err == nil || grade.State != "unknown" || grade.Passed != nil || f.runtime.gradeCalls != 1 {
		t.Fatalf("lost canonical result was presented as known: %+v %v", grade, err)
	}
	again, err := f.tasks.GradeEvaluation(ctx, command)
	if err != nil || again.State != "unknown" || again.Passed != nil || !again.AlreadyRecorded || again.ResultEventID != "" || f.runtime.gradeCalls != 1 {
		t.Fatalf("pending intent was replayed: %+v %v", again, err)
	}
	if session.ModelTaskIdle(ctx, f.ledger, f.actor) == nil || session.ModelTaskYieldable(ctx, f.ledger, f.actor) == nil || session.ModelTaskAdmissionAbandonable(ctx, f.ledger, f.actor) == nil {
		t.Fatal("pending grader runtime was treated as stopped by a model task release path")
	}
	if _, err := f.tasks.ModelWorkspace(ctx, 7, f.id); err == nil {
		t.Fatal("a pending hidden grading intent admitted a new model turn")
	}
}

func TestEvaluationGradeUnknownRuntimeRetainsOriginalOperation(t *testing.T) {
	f := prepareGraderFixture(t)
	f.runtime.unknown = true
	ctx := context.Background()
	checks := []byte(`{"schema_version":1,"files":[{"path":"answer.json","contains":"visible-output"}]}`)
	path := filepath.Join(t.TempDir(), "checks.json")
	if err := os.WriteFile(path, checks, 0600); err != nil {
		t.Fatal(err)
	}
	events, _ := f.ledger.Events(ctx, f.id)
	command := session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "unknown-runtime", ExpectedSessionHeadChecksum: events[len(events)-1].Checksum, ChecksPath: path, ChecksSHA256: graderHash(checks)}
	grade, err := f.tasks.GradeEvaluation(ctx, command)
	if err != nil || grade.State != "unknown" || grade.Passed != nil || grade.Stopped || grade.RuntimeID == "" || !strings.HasPrefix(grade.RuntimeID, grade.OperationID+"/") || f.runtime.closed != 1 {
		t.Fatalf("uncertain remote runtime was cleared: %+v %v", grade, err)
	}
	again, err := f.tasks.GradeEvaluation(ctx, command)
	if err != nil || !again.AlreadyRecorded || again.RuntimeID != grade.RuntimeID || again.OperationID != grade.OperationID || again.ResultEventID != grade.ResultEventID || f.runtime.gradeCalls != 1 {
		t.Fatal("unknown remote grading operation was replaced or dispatched again")
	}
	if session.ModelTaskIdle(ctx, f.ledger, f.actor) == nil || session.ModelTaskYieldable(ctx, f.ledger, f.actor) == nil || session.ModelTaskAdmissionAbandonable(ctx, f.ledger, f.actor) == nil {
		t.Fatal("an unconfirmed remote grader released its task")
	}
}

func TestEvaluationGradeKeepsTypedRuntimeFailureStagesAndStopProof(t *testing.T) {
	for _, tc := range []struct {
		outcome, state, reason string
		code                   *int
	}{
		{outcome: "unavailable", state: "scorer_error", reason: "grader_runtime_unavailable"},
		{outcome: "stopped-unknown", state: "unknown", reason: "grader_runtime_outcome_unknown"},
		{outcome: "nonzero", state: "scorer_error", reason: "grader_execution_failed", code: func() *int { n := 7; return &n }()},
		{outcome: "parser-bad", state: "scorer_error", reason: "grader_output_invalid", code: func() *int { n := 0; return &n }()},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			f := prepareGraderFixture(t)
			f.runtime.outcome = tc.outcome
			ctx := context.Background()
			checks := []byte(`{"schema_version":1,"files":[{"path":"answer.json","contains":"visible-output"}]}`)
			path := filepath.Join(t.TempDir(), "checks.json")
			if err := os.WriteFile(path, checks, 0600); err != nil {
				t.Fatal(err)
			}
			events, _ := f.ledger.Events(ctx, f.id)
			command := session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "typed-runtime-failure", ExpectedSessionHeadChecksum: events[len(events)-1].Checksum, ChecksPath: path, ChecksSHA256: graderHash(checks)}
			grade, err := f.tasks.GradeEvaluation(ctx, command)
			if err != nil || grade.State != tc.state || grade.Reason != tc.reason || !grade.Stopped || grade.Passed != nil || grade.IntentEventID == "" || grade.ResultEventID == "" || f.runtime.gradeCalls != 1 || (grade.ExitCode == nil) != (tc.code == nil) || tc.code != nil && int(*grade.ExitCode) != *tc.code {
				t.Fatalf("runtime failure lost its stage/known stop: %+v %v", grade, err)
			}
			stored, _ := f.ledger.Events(ctx, f.id)
			projected, err := session.ReadEvaluationGrade(stored)
			if err != nil || projected == nil || projected.State != grade.State || projected.Reason != grade.Reason || projected.RuntimeID != grade.RuntimeID || projected.ResultEventChecksum != grade.ResultEventChecksum || !projected.Stopped {
				t.Fatalf("typed failure did not survive canonical re-read: %+v %v", projected, err)
			}
			again, err := f.tasks.GradeEvaluation(ctx, command)
			if err != nil || !again.AlreadyRecorded || again.State != grade.State || again.ResultEventID != grade.ResultEventID || again.RuntimeID != grade.RuntimeID || f.runtime.gradeCalls != 1 {
				t.Fatal("failed or unknown grader was dispatched again")
			}
			after, _ := f.ledger.Events(ctx, f.id)
			if len(after) != len(stored) || session.ModelTaskIdle(ctx, f.ledger, f.actor) == nil || session.ModelTaskYieldable(ctx, f.ledger, f.actor) == nil {
				t.Fatal("a noncompleted grade mutated history or admitted normal finalization")
			}
			if session.ModelTaskAdmissionAbandonable(ctx, f.ledger, f.actor) != nil {
				t.Fatal("the retained exact stop proof did not allow explicit safe abandonment")
			}
			for _, event := range after {
				if strings.Contains(string(event.Payload), "RUNTIME_PRIVATE_") {
					t.Fatal("raw runtime diagnostics leaked into canonical facts")
				}
			}
		})
	}
}

func TestEvaluationGradeStrictAssertionsAndOperatorBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		checks    string
		wantState string
		wantError bool
	}{
		{"integer-is-not-float", `{"schema_version":1,"files":[{"path":"answer.json","json_equals":{"label":"visible-output","flag":true,"number":1.0}}]}`, "completed", false},
		{"boolean-is-not-integer", `{"schema_version":1,"files":[{"path":"answer.json","json_equals":{"label":"visible-output","flag":1,"number":1}}]}`, "completed", false},
		{"hidden-negative-assertion", `{"schema_version":1,"files":[{"path":"answer.json","contains":"OPERATOR_ONLY_GOLD_NEVER_IN_SESSION"}]}`, "completed", false},
		{"missing-candidate-file", `{"schema_version":1,"files":[{"path":"absent.json","json_equals":null}]}`, "completed", false},
		{"parent-path", `{"schema_version":1,"files":[{"path":"../checks.json","contains":"hidden"}]}`, "scorer_error", false},
		{"ads-path", `{"schema_version":1,"files":[{"path":"answer.json:stream","contains":"hidden"}]}`, "scorer_error", false},
		{"duplicate-key", `{"schema_version":1,"files":[{"path":"answer.json","path":"public.txt","contains":"hidden"}]}`, "scorer_error", false},
		{"boolean-schema", `{"schema_version":true,"files":[{"path":"answer.json","contains":"hidden"}]}`, "blocked", true},
		{"fraction-schema", `{"schema_version":1.0,"files":[{"path":"answer.json","contains":"hidden"}]}`, "blocked", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := prepareGraderFixture(t)
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "checks.json")
			if err := os.WriteFile(path, []byte(tc.checks), 0600); err != nil {
				t.Fatal(err)
			}
			events, _ := f.ledger.Events(ctx, f.id)
			grade, err := f.tasks.GradeEvaluation(ctx, session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "strict-checks", ExpectedSessionHeadChecksum: events[len(events)-1].Checksum, ChecksPath: path, ChecksSHA256: graderHash([]byte(tc.checks))})
			if (err != nil) != tc.wantError || grade.State != tc.wantState {
				t.Fatalf("strict grading result: %+v %v", grade, err)
			}
			if tc.wantState == "completed" {
				if grade.Passed == nil || *grade.Passed || grade.ChecksPassed == nil || *grade.ChecksPassed != 0 || grade.ChecksTotal == nil || *grade.ChecksTotal != 1 {
					t.Fatalf("known negative assertion was accepted: %+v", grade)
				}
			} else if grade.Passed != nil || grade.ChecksPassed != nil {
				t.Fatal("invalid operator assertions became an Agent verdict")
			}
			if tc.wantError && f.runtime.gradeCalls != 0 {
				t.Fatal("invalid schema reached the runtime")
			}
			events, _ = f.ledger.Events(ctx, f.id)
			for _, event := range events {
				if strings.Contains(string(event.Payload), "OPERATOR_ONLY_GOLD_NEVER_IN_SESSION") {
					t.Fatal("hidden negative expectation leaked into Session Ledger")
				}
			}
		})
	}
}

func TestEvaluationGradeRejectsSymlinkedOperatorChecks(t *testing.T) {
	f := prepareGraderFixture(t)
	ctx := context.Background()
	checks := []byte(`{"schema_version":1,"files":[{"path":"answer.json","contains":"visible-output"}]}`)
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target.json"), filepath.Join(dir, "checks.json")
	if err := os.WriteFile(target, checks, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
	events, _ := f.ledger.Events(ctx, f.id)
	_, err := f.tasks.GradeEvaluation(ctx, session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "symlink-checks", ExpectedSessionHeadChecksum: events[len(events)-1].Checksum, ChecksPath: link, ChecksSHA256: graderHash(checks)})
	if err == nil || f.runtime.gradeCalls != 0 {
		t.Fatal("redirected operator checks were read or dispatched")
	}
}

func graderHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestEvaluationGradeBindsFrozenCandidateAndReplaysOnlyKnownResult(t *testing.T) {
	f := prepareGraderFixture(t)
	ctx := context.Background()
	checks := []byte(`{"schema_version":1,"files":[{"path":"answer.json","contains":"visible-output"},{"path":"answer.json","json_equals":{"label":"visible-output","flag":true,"number":1}},{"path":"answer.json","sha256":"` + strings.ToUpper(graderHash([]byte(`{"label":"visible-output","flag":true,"number":1}`))) + `"}]}`)
	checksPath := filepath.Join(t.TempDir(), "operator-only-checks.json")
	if err := os.WriteFile(checksPath, checks, 0600); err != nil {
		t.Fatal(err)
	}
	events, _ := f.ledger.Events(ctx, f.id)
	command := session.EvaluationGradeCommand{OwnerID: 7, Actor: f.actor, SessionID: f.id, RequestID: "fixture-grade", ExpectedSessionHeadChecksum: events[len(events)-1].Checksum, ChecksPath: checksPath, ChecksSHA256: graderHash(checks)}
	grade, err := f.tasks.GradeEvaluation(ctx, command)
	if err != nil || grade.State != "completed" || grade.Passed == nil || !*grade.Passed || grade.ChecksTotal == nil || *grade.ChecksTotal != 3 || grade.ChecksPassed == nil || *grade.ChecksPassed != 3 || !grade.Stopped || grade.ExitCode == nil || *grade.ExitCode != 0 {
		t.Fatalf("known worked example was not graded: %+v %v", grade, err)
	}
	if grade.CandidateSHA256 != f.proposal.Proposal.FinalStateSHA256 || grade.InputSHA256 == "" || grade.CheckerSHA256 == "" || grade.OutputSHA256 == "" || grade.CandidateArchiveSHA256 == "" || grade.IntentEventID == "" || grade.ResultEventID == "" || f.runtime.gradeCalls != 1 {
		t.Fatal("grade lost candidate, input, checker, runtime or canonical provenance")
	}
	if f.runtime.gradeDir == f.actual || !strings.HasPrefix(f.runtime.gradeDir, f.storage+string(filepath.Separator)) {
		t.Fatal("hidden grader was placed inside the Agent workspace")
	}
	after, _ := f.ledger.Events(ctx, f.id)
	if after[len(after)-1].EventID != grade.ResultEventID || after[len(after)-1].Checksum != grade.ResultEventChecksum {
		t.Fatal("grade receipt does not identify the canonical result head")
	}
	for _, event := range after {
		if strings.HasPrefix(event.Type, "evaluation/grade-") && (strings.Contains(string(event.Payload), "json_equals") || strings.Contains(string(event.Payload), checksPath)) {
			t.Fatal("operator checks leaked into Session facts")
		}
	}
	again, err := f.tasks.GradeEvaluation(ctx, command)
	if err != nil || !again.AlreadyRecorded || again.ResultEventID != grade.ResultEventID || f.runtime.gradeCalls != 1 {
		t.Fatalf("known result was rerun: %+v %v", again, err)
	}
	encoded, _ := json.Marshal(grade)
	if strings.Contains(string(encoded), "visible-output") || strings.Contains(string(encoded), checksPath) {
		t.Fatal("operator checks leaked into the ordinary grade result")
	}
}
