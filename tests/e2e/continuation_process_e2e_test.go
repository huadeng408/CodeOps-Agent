package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/identity"
	harnessorch "code-agent/internal/orchestrator"
	"code-agent/internal/session"
)

const continuationProcessHelperEnv = "CODE_AGENT_CONTINUATION_PROCESS_HELPER"

type continuationProcessControl struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
}

// blockingToolResultLog commits the result, then holds the first Go process
// before it can return the receipt to Python. This creates the exact crash
// window where the external side effect and Harness receipt are durable while
// Python still has a model_after checkpoint.
type blockingToolResultLog struct {
	session.EventLog
	readyPath string
}

func (l *blockingToolResultLog) AppendSurface(
	ctx context.Context,
	sessionID string,
	expectedSeq int64,
	eventType string,
	payload any,
	operation session.SurfaceOperation,
) (session.Event, error) {
	event, err := l.EventLog.AppendSurface(ctx, sessionID, expectedSeq, eventType, payload, operation)
	if err != nil || eventType != "tool/result" {
		return event, err
	}
	if writeErr := os.WriteFile(l.readyPath, []byte(event.Checksum+"\n"), 0o600); writeErr != nil {
		return session.Event{}, writeErr
	}
	select {}
}

func TestContinuationSurvivesGoAndPythonRestartWithoutRepeatingCommittedTool(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_CONTINUATION_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_CONTINUATION_E2E=1 to run continuation process recovery E2E")
	}

	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := t.TempDir()
	databasePath := filepath.Join(projectRoot, "sessions.sqlite")
	memoryDir := filepath.Join(projectRoot, "memory")
	controlPath := filepath.Join(projectRoot, "continuation.json")
	readyPath := filepath.Join(projectRoot, "tool-result-committed")
	toolCallsPath := filepath.Join(projectRoot, "tool-executions.log")
	address := contextE2EAddress(t)
	python := contextE2EPython(t)

	firstPython, firstPythonOutput := startContinuationPython(
		t, python, repositoryRoot, projectRoot, memoryDir, address,
	)
	t.Cleanup(func() { stopSessionControlServer(firstPython) })
	waitForTCP(t, address, 20*time.Second, firstPythonOutput)
	firstGo := startContinuationGoHelper(
		t, "first", repositoryRoot, address, databasePath, controlPath, readyPath, toolCallsPath,
	)
	t.Cleanup(func() { stopSessionControlServer(firstGo) })
	control := waitForContinuationControl(t, controlPath, 20*time.Second)
	waitForFile(t, readyPath, 20*time.Second)

	if err := firstGo.Process.Kill(); err != nil {
		t.Fatalf("terminate first Go continuation process: %v", err)
	}
	if err := firstGo.Wait(); err == nil {
		t.Fatal("first Go continuation process exited cleanly; crash recovery was not exercised")
	}
	stopSessionControlServer(firstPython)

	firstAttemptEvents := readContinuationEvents(t, databasePath, control.SessionID)
	if countEventType(firstAttemptEvents, "tool/result") != 1 {
		t.Fatalf("first process tool result count = %d, want 1", countEventType(firstAttemptEvents, "tool/result"))
	}
	if terminalCount(firstAttemptEvents) != 0 {
		t.Fatalf("first process wrote a terminal receipt before recovery: %+v", firstAttemptEvents)
	}

	secondPython, secondPythonOutput := startContinuationPython(
		t, python, repositoryRoot, projectRoot, memoryDir, address,
	)
	t.Cleanup(func() { stopSessionControlServer(secondPython) })
	waitForTCP(t, address, 20*time.Second, secondPythonOutput)
	secondGo := runContinuationGoHelper(
		t, "recover", repositoryRoot, address, databasePath, controlPath, readyPath, toolCallsPath,
	)
	stopSessionControlServer(secondPython)
	checkpointResumedCount := countContextStatus(
		t,
		filepath.Join(projectRoot, ".agent", "context.sqlite"),
		control.SessionID,
		"checkpoint_resumed",
	)
	if checkpointResumedCount != 1 {
		t.Fatalf("Python checkpoint_resumed events = %d, want exactly 1", checkpointResumedCount)
	}

	toolCalls, err := os.ReadFile(toolCallsPath)
	if err != nil {
		t.Fatalf("read tool execution evidence: %v", err)
	}
	if got := len(strings.Fields(string(toolCalls))); got != 1 {
		t.Fatalf("external tool executions = %d, want exactly 1; calls=%q", got, toolCalls)
	}

	log, err := session.OpenSQLiteEventLog(databasePath)
	if err != nil {
		t.Fatalf("open recovered continuation ledger: %v", err)
	}
	events, err := log.Events(context.Background(), control.SessionID)
	if err != nil {
		_ = log.Close()
		t.Fatalf("read recovered continuation ledger: %v", err)
	}
	if err := log.Verify(context.Background(), control.SessionID); err != nil {
		_ = log.Close()
		t.Fatalf("verify recovered continuation ledger: %v", err)
	}
	surface, err := log.Surface(context.Background(), control.SessionID)
	if err != nil {
		_ = log.Close()
		t.Fatalf("read recovered continuation Surface: %v", err)
	}
	workbench := session.NewWorkbench(log, nil)
	view, err := workbench.Get(context.Background(), 7, control.SessionID)
	if err != nil {
		_ = log.Close()
		t.Fatalf("project recovered continuation: %v", err)
	}

	checks := map[string]int{
		"session/continued": countEventType(events, "session/continued"),
		"tool/call":         countEventType(events, "tool/call"),
		"tool/result":       countEventType(events, "tool/result"),
		"assistant/message": countRunAssistant(events, control.RunID),
		"run/completed":     countEventType(events, "session/run-completed"),
		"run/failed":        countEventType(events, "session/run-failed"),
	}
	if checks["session/continued"] != 1 || checks["tool/call"] != 1 || checks["tool/result"] != 1 || checks["assistant/message"] != 1 {
		_ = log.Close()
		t.Fatalf("continuation receipt counts = %+v, want one durable fact of each kind", checks)
	}
	if checks["run/completed"] != 1 || checks["run/failed"] != 0 || terminalCount(events) != 1 {
		_ = log.Close()
		t.Fatalf("terminal receipt counts = %+v, want exactly one successful terminal", checks)
	}
	if view.Status != "done" || view.Run == nil || view.Run.RunID != control.RunID || view.Run.Status != session.RunCompleted || view.Run.Attempt != 2 {
		_ = log.Close()
		t.Fatalf("recovered run projection = %+v", view)
	}
	if len(surface) != 4 || surface[0].Type != "user/message" || surface[1].Type != "tool/call" || surface[2].Type != "tool/result" || surface[3].Type != "assistant/message" {
		_ = log.Close()
		t.Fatalf("recovered Surface = %+v", surface)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close recovered continuation ledger: %v", err)
	}

	receipt := map[string]any{
		"status":                           "VERIFIED",
		"kind":                             "continuation-go-python-process-recovery",
		"git_sha":                          productionGitSHA(t, repositoryRoot),
		"command":                          "CODE_AGENT_RUN_CONTINUATION_E2E=1 go test ./tests/e2e -run TestContinuationSurvivesGoAndPythonRestartWithoutRepeatingCommittedTool -count=1",
		"session_id":                       control.SessionID,
		"run_id":                           control.RunID,
		"first_go_pid":                     firstGo.Process.Pid,
		"first_go_exit_code":               firstGo.ProcessState.ExitCode(),
		"second_go_pid":                    secondGo.Process.Pid,
		"second_go_exit_code":              secondGo.ProcessState.ExitCode(),
		"first_python_pid":                 firstPython.Process.Pid,
		"second_python_pid":                secondPython.Process.Pid,
		"run_attempts":                     view.Run.Attempt,
		"external_tool_executions":         1,
		"terminal_receipt_count":           terminalCount(events),
		"completed_receipt_count":          checks["run/completed"],
		"failed_receipt_count":             checks["run/failed"],
		"tool_call_receipt_count":          checks["tool/call"],
		"tool_result_receipt_count":        checks["tool/result"],
		"assistant_receipt_count":          checks["assistant/message"],
		"event_count":                      len(events),
		"surface_event_count":              len(surface),
		"sequence_contiguous":              sessionEventSequenceContiguous(events),
		"final_event_checksum":             events[len(events)-1].Checksum,
		"database_sha256":                  productionFileSHA256(t, databasePath),
		"python_checkpoint_resumed_events": checkpointResumedCount,
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode continuation recovery receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "continuation-process-recovery.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create continuation receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write continuation recovery receipt: %v", err)
	}
}

// TestContinuationRunnerProcessHelper is executed in a separate Go process by
// the opt-in E2E above. It is skipped during ordinary test collection.
func TestContinuationRunnerProcessHelper(t *testing.T) {
	if os.Getenv(continuationProcessHelperEnv) != "1" {
		t.Skip("continuation process helper")
	}
	stage := strings.TrimSpace(os.Getenv("CODE_AGENT_CONTINUATION_STAGE"))
	databasePath := os.Getenv("CODE_AGENT_CONTINUATION_DB")
	controlPath := os.Getenv("CODE_AGENT_CONTINUATION_CONTROL")
	readyPath := os.Getenv("CODE_AGENT_CONTINUATION_READY")
	toolCallsPath := os.Getenv("CODE_AGENT_CONTINUATION_TOOL_CALLS")
	address := os.Getenv("CODE_AGENT_CONTINUATION_ADDR")

	sqliteLog, err := session.OpenSQLiteEventLog(databasePath)
	if err != nil {
		t.Fatalf("open helper continuation ledger: %v", err)
	}
	var eventLog session.EventLog = sqliteLog
	if stage == "first" {
		eventLog = &blockingToolResultLog{EventLog: sqliteLog, readyPath: readyPath}
	}
	workbench := session.NewWorkbench(eventLog, nil)
	client, err := harnessorch.NewClient(address)
	if err != nil {
		t.Fatalf("create helper orchestrator client: %v", err)
	}
	client.SetConversationTimeout(20 * time.Second)
	toolExecutor := session.ToolExecutionFunc(func(
		_ context.Context,
		_ identity.Actor,
		_ string,
		call harnessorch.ToolCall,
	) harnessorch.ToolResult {
		file, openErr := os.OpenFile(toolCallsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if openErr != nil {
			return harnessorch.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: openErr.Error(), ExitCode: 1}
		}
		_, writeErr := file.WriteString(call.ID + "\n")
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return harnessorch.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "write side-effect evidence failed", ExitCode: 1}
		}
		return harnessorch.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Output: "durable tool result"}
	})
	runner := session.NewSessionRunner(workbench, client, toolExecutor, session.SessionRunnerOptions{
		WorkerID: "continuation-" + stage, LeaseDuration: 350 * time.Millisecond, HeartbeatInterval: 75 * time.Millisecond,
	})
	defer func() {
		_ = runner.Close()
		_ = client.Close()
		_ = sqliteLog.Close()
	}()

	switch stage {
	case "first":
		created, createErr := workbench.Create(context.Background(), 7, "continuation-e2e", "restart recovery", "continue exactly once")
		if createErr != nil {
			t.Fatal(createErr)
		}
		message, appendErr := workbench.AppendUserMessage(context.Background(), 7, created.ID, 1, "CREATE_CONTEXT_RECOVERY_MARKER")
		if appendErr != nil {
			t.Fatal(appendErr)
		}
		checkpoint, checkpointErr := workbench.CreateCheckpoint(context.Background(), 7, created.ID, 2, message.ID, "before tool")
		if checkpointErr != nil {
			t.Fatal(checkpointErr)
		}
		if statusErr := workbench.UpdateStatus(context.Background(), 7, created.ID, 3, "paused"); statusErr != nil {
			t.Fatal(statusErr)
		}
		accepted, requestErr := runner.RequestContinuation(context.Background(), session.ContinueCommand{
			RequestID: "process-restart-request", SessionID: created.ID, CheckpointHash: checkpoint.Hash,
			OwnerID: 7, ExpectedSeq: 4, Actor: continuationProcessActor(),
		})
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		writeContinuationControl(t, controlPath, continuationProcessControl{SessionID: created.ID, RunID: accepted.RunID})
		select {}
	case "recover":
		control := readContinuationControl(t, controlPath)
		if recoverErr := runner.Recover(context.Background()); recoverErr != nil {
			t.Fatalf("recover continuation runs: %v", recoverErr)
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			view, runErr := runner.Run(context.Background(), control.SessionID, control.RunID)
			if runErr == nil && view.Status == session.RunCompleted {
				return
			}
			if runErr == nil && view.Status == session.RunFailed {
				t.Fatalf("recovered continuation failed: %+v", view)
			}
			time.Sleep(25 * time.Millisecond)
		}
		view, runErr := runner.Run(context.Background(), control.SessionID, control.RunID)
		t.Fatalf("continuation recovery timed out: view=%+v err=%v", view, runErr)
	default:
		t.Fatalf("unknown continuation helper stage %q", stage)
	}
}

func continuationProcessActor() identity.Actor {
	return identity.Actor{
		SchemaVersion: 1, ActorID: "user:7", Subject: "continuation-e2e",
		TenantID: "tenant:e2e", Roles: []string{"USER"},
	}
}

func startContinuationPython(
	t *testing.T,
	python, repositoryRoot, projectRoot, memoryDir, address string,
) (*exec.Cmd, *strings.Builder) {
	t.Helper()
	host, port, found := strings.Cut(address, ":")
	if !found {
		t.Fatalf("invalid continuation orchestrator address %q", address)
	}
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "context_runtime_server.py")
	command := exec.Command(
		python, fixture,
		"--host", host,
		"--port", port,
		"--project-root", projectRoot,
		"--working-dir", projectRoot,
		"--memory-dir", memoryDir,
	)
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "PYTHONPATH="+repositoryRoot)
	output := &strings.Builder{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatalf("start continuation Python orchestrator: %v", err)
	}
	return command, output
}

func startContinuationGoHelper(
	t *testing.T,
	stage, repositoryRoot, address, databasePath, controlPath, readyPath, toolCallsPath string,
) *exec.Cmd {
	t.Helper()
	command := continuationGoHelperCommand(stage, repositoryRoot, address, databasePath, controlPath, readyPath, toolCallsPath)
	output := &strings.Builder{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatalf("start %s Go continuation helper: %v", stage, err)
	}
	return command
}

func runContinuationGoHelper(
	t *testing.T,
	stage, repositoryRoot, address, databasePath, controlPath, readyPath, toolCallsPath string,
) *exec.Cmd {
	t.Helper()
	command := continuationGoHelperCommand(stage, repositoryRoot, address, databasePath, controlPath, readyPath, toolCallsPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s Go continuation helper: %v; output=%q", stage, err, output)
	}
	return command
}

func continuationGoHelperCommand(
	stage, repositoryRoot, address, databasePath, controlPath, readyPath, toolCallsPath string,
) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestContinuationRunnerProcessHelper$", "-test.v")
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(),
		continuationProcessHelperEnv+"=1",
		"CODE_AGENT_CONTINUATION_STAGE="+stage,
		"CODE_AGENT_CONTINUATION_ADDR="+address,
		"CODE_AGENT_CONTINUATION_DB="+databasePath,
		"CODE_AGENT_CONTINUATION_CONTROL="+controlPath,
		"CODE_AGENT_CONTINUATION_READY="+readyPath,
		"CODE_AGENT_CONTINUATION_TOOL_CALLS="+toolCallsPath,
	)
	return command
}

func writeContinuationControl(t *testing.T, path string, control continuationProcessControl) {
	t.Helper()
	encoded, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readContinuationControl(t *testing.T, path string) continuationProcessControl {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var control continuationProcessControl
	if err := json.Unmarshal(encoded, &control); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(control.SessionID) == "" || strings.TrimSpace(control.RunID) == "" {
		t.Fatalf("invalid continuation process control: %+v", control)
	}
	return control
}

func waitForContinuationControl(t *testing.T, path string, timeout time.Duration) continuationProcessControl {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		encoded, err := os.ReadFile(path)
		if err == nil {
			var control continuationProcessControl
			if json.Unmarshal(encoded, &control) == nil && control.SessionID != "" && control.RunID != "" {
				return control
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read continuation process control: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("continuation process control was not written within %s", timeout)
	return continuationProcessControl{}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("file %s was not written within %s", path, timeout)
}

func readContinuationEvents(t *testing.T, databasePath, sessionID string) []session.Event {
	t.Helper()
	log, err := session.OpenSQLiteEventLog(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	events, err := log.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func terminalCount(events []session.Event) int {
	return countEventType(events, "session/run-completed") + countEventType(events, "session/run-failed")
}

func countRunAssistant(events []session.Event, runID string) int {
	count := 0
	for _, event := range events {
		if event.Type != "assistant/message" {
			continue
		}
		var payload struct {
			RunID string `json:"run_id"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil && payload.RunID == runID {
			count++
		}
	}
	return count
}

func countContextStatus(t *testing.T, databasePath, sessionID, status string) int {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open Python Context store: %v", err)
	}
	defer database.Close()
	rows, err := database.Query(
		"SELECT payload_json FROM context_events WHERE session_id = ? AND kind = ? ORDER BY sequence",
		sessionID,
		"execution_result",
	)
	if err != nil {
		t.Fatalf("read Python Context events: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan Python Context event: %v", err)
		}
		var payload map[string]any
		if json.Unmarshal([]byte(raw), &payload) == nil && payload["status"] == status {
			count++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate Python Context events: %v", err)
	}
	return count
}
