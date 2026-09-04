package e2e_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/session"

	_ "modernc.org/sqlite"
)

func TestProductionAgentSessionSurvivesProcessTermination(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_SESSION_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_SESSION_E2E=1 to run the production session process E2E")
	}

	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agent"), 0o755); err != nil {
		t.Fatalf("create E2E project: %v", err)
	}
	databasePath := filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite")
	settings := map[string]any{
		"session_db_path":         databasePath,
		"memory_dir":              filepath.Join(projectRoot, ".agent", "memory"),
		"orchestrator_addr":       "127.0.0.1:1",
		"orchestrator_auto_start": false,
		"mcp_config":              "missing-mcp.json",
		"sandbox":                 map[string]any{"enabled": false},
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("encode E2E settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agent", "settings.local.json"), settingsJSON, 0o600); err != nil {
		t.Fatalf("write E2E settings: %v", err)
	}

	agentBinary := buildProductionAgent(t, repositoryRoot)
	reader, writer := io.Pipe()
	first := exec.Command(agentBinary)
	first.Dir = projectRoot
	first.Stdin = reader
	var firstStdout, firstStderr strings.Builder
	first.Stdout = &firstStdout
	first.Stderr = &firstStderr
	if err := first.Start(); err != nil {
		t.Fatalf("start first production agent: %v", err)
	}
	if _, err := io.WriteString(writer, "/plan\npersist across termination\n"); err != nil {
		_ = first.Process.Kill()
		_ = first.Wait()
		t.Fatalf("send first session input: %v", err)
	}

	sessionID, eventCount := waitForSessionState(t, databasePath, 2, "plan")
	if err := first.Process.Kill(); err != nil {
		t.Fatalf("terminate first production agent: %v", err)
	}
	_ = writer.Close()
	if err := first.Wait(); err == nil {
		t.Fatal("first production agent exited cleanly; E2E did not exercise termination recovery")
	}

	log, err := session.OpenSQLiteEventLog(databasePath)
	if err != nil {
		t.Fatalf("open event log after termination: %v", err)
	}
	defer log.Close()
	events, err := log.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("read events after termination: %v", err)
	}
	if len(events) != eventCount || len(events) < 4 {
		t.Fatalf("event count after termination = %d, want stable count >=4 (observed %d)", len(events), eventCount)
	}
	if err := log.Verify(context.Background(), sessionID); err != nil {
		t.Fatalf("verify event hash chain after termination: %v", err)
	}
	var persisted session.Session
	if err := json.Unmarshal(events[len(events)-1].Payload, &persisted); err != nil {
		t.Fatalf("decode persisted session state: %v", err)
	}
	if persisted.ID != sessionID || len(persisted.Messages) != 2 {
		t.Fatalf("persisted session = id %q with %d messages, want id %q and 2 messages", persisted.ID, len(persisted.Messages), sessionID)
	}
	if persisted.Mode != "plan" {
		t.Fatalf("persisted mode = %q, want plan", persisted.Mode)
	}
	if persisted.Messages[0].Content != "persist across termination" {
		t.Fatalf("persisted user message = %q", persisted.Messages[0].Content)
	}

	second := exec.Command(agentBinary)
	second.Dir = projectRoot
	second.Stdin = strings.NewReader("/resume " + sessionID + "\n")
	var secondStdout, secondStderr strings.Builder
	second.Stdout = &secondStdout
	second.Stderr = &secondStderr
	if err := second.Run(); err != nil {
		t.Fatalf("resume session in independent production agent: %v", err)
	}
	if !strings.Contains(secondStdout.String(), fmt.Sprintf("resumed session %s with 2 messages", sessionID)) {
		t.Fatalf("independent agent did not report resumed session %s", sessionID)
	}

	recovered, err := log.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("read recovered events: %v", err)
	}
	if len(recovered) != len(events)+1 {
		t.Fatalf("recovered event count = %d, want %d", len(recovered), len(events)+1)
	}
	if err := log.Verify(context.Background(), sessionID); err != nil {
		t.Fatalf("verify recovered event hash chain: %v", err)
	}
	surface, err := log.Surface(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("replay recovered session surface: %v", err)
	}
	if !sessionEventSequenceContiguous(recovered) {
		t.Fatal("recovered event sequence is not contiguous")
	}
	var legacyTables int
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open recovered SQLite database: %v", err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&legacyTables); err != nil {
		t.Fatalf("inspect recovered legacy table: %v", err)
	}
	if legacyTables != 0 {
		t.Fatalf("production session E2E created mutable legacy sessions table")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close recovered SQLite database: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close recovered event log: %v", err)
	}

	receipt := map[string]any{
		"kind":                 "production-session-process-recovery",
		"git_sha":              productionGitSHA(t, repositoryRoot),
		"command":              "CODE_AGENT_RUN_SESSION_E2E=1 go test ./tests/e2e -run TestProductionAgentSessionSurvivesProcessTermination -count=1",
		"schema_version":       events[0].Version,
		"event_version":        events[0].Version,
		"session_id":           sessionID,
		"event_count":          len(recovered),
		"first_process_pid":    first.Process.Pid,
		"first_exit_code":      first.ProcessState.ExitCode(),
		"second_process_pid":   second.ProcessState.Pid(),
		"second_exit_code":     second.ProcessState.ExitCode(),
		"legacy_table_count":   legacyTables,
		"sequence_contiguous":  sessionEventSequenceContiguous(recovered),
		"surface_event_count":  len(surface),
		"state_projection":     sessionStateReceipt(persisted),
		"final_event_checksum": recovered[len(recovered)-1].Checksum,
		"database_sha256":      productionFileSHA256(t, databasePath),
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode session E2E receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "session-process-recovery.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create session E2E receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write session E2E receipt: %v", err)
	}
	t.Logf("session E2E receipt written to ignored runtime path")
}

func TestProductionAgentForkAndRewindSurviveProcessBoundary(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_SESSION_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_SESSION_E2E=1 to run the production session fork/rewind E2E")
	}

	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agent"), 0o755); err != nil {
		t.Fatalf("create E2E project: %v", err)
	}
	databasePath := filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite")
	settings := map[string]any{
		"session_db_path":         databasePath,
		"memory_dir":              filepath.Join(projectRoot, ".agent", "memory"),
		"orchestrator_addr":       "127.0.0.1:1",
		"orchestrator_auto_start": false,
		"mcp_config":              "missing-mcp.json",
		"sandbox":                 map[string]any{"enabled": false},
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("encode E2E settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agent", "settings.local.json"), settingsJSON, 0o600); err != nil {
		t.Fatalf("write E2E settings: %v", err)
	}

	agentBinary := buildProductionAgent(t, repositoryRoot)
	reader, writer := io.Pipe()
	first := exec.Command(agentBinary)
	first.Dir = projectRoot
	first.Stdin = reader
	var firstStdout, firstStderr strings.Builder
	first.Stdout = &firstStdout
	first.Stderr = &firstStderr
	if err := first.Start(); err != nil {
		t.Fatalf("start first production agent: %v", err)
	}
	if _, err := io.WriteString(writer, "/plan\nfork source message\n"); err != nil {
		_ = first.Process.Kill()
		_ = first.Wait()
		t.Fatalf("send first session input: %v", err)
	}

	sessionID, eventCount := waitForSessionState(t, databasePath, 1, "plan")
	if err := first.Process.Kill(); err != nil {
		t.Fatalf("terminate first production agent: %v", err)
	}
	_ = writer.Close()
	if err := first.Wait(); err == nil {
		t.Fatal("first production agent exited cleanly; E2E did not exercise termination")
	}

	log, err := session.OpenSQLiteEventLog(databasePath)
	if err != nil {
		t.Fatalf("open event log after termination: %v", err)
	}
	parentBeforeResume, err := log.Events(context.Background(), sessionID)
	if err != nil {
		_ = log.Close()
		t.Fatalf("read parent events before fork: %v", err)
	}
	targetSeq := sessionStateWithMessageCount(t, parentBeforeResume, 1)
	if err := log.Close(); err != nil {
		t.Fatalf("close parent event log before second agent: %v", err)
	}

	second := exec.Command(agentBinary)
	second.Dir = projectRoot
	second.Stdin = strings.NewReader(fmt.Sprintf("/resume %s\n/fork child-session-e2e %d\n/rewind %d\n", sessionID, targetSeq, targetSeq))
	var secondStdout, secondStderr strings.Builder
	second.Stdout = &secondStdout
	second.Stderr = &secondStderr
	if err := second.Run(); err != nil {
		t.Fatalf("run fork/rewind commands in independent agent: %v", err)
	}
	if !strings.Contains(secondStdout.String(), fmt.Sprintf("forked session child-session-e2e from event %d", targetSeq)) {
		t.Fatalf("fork output = %q", secondStdout.String())
	}
	if !strings.Contains(secondStdout.String(), fmt.Sprintf("rewound session child-session-e2e to event %d", targetSeq)) {
		t.Fatalf("rewind output = %q", secondStdout.String())
	}

	log, err = session.OpenSQLiteEventLog(databasePath)
	if err != nil {
		t.Fatalf("open event log after fork/rewind: %v", err)
	}
	defer log.Close()
	parentEvents, err := log.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("read parent events: %v", err)
	}
	childEvents, err := log.Events(context.Background(), "child-session-e2e")
	if err != nil {
		t.Fatalf("read child events: %v", err)
	}
	if len(parentEvents) < eventCount || len(childEvents) < 6 {
		t.Fatalf("event counts parent=%d child=%d, want parent>=%d and child>=6", len(parentEvents), len(childEvents), eventCount)
	}
	if err := log.Verify(context.Background(), sessionID); err != nil {
		t.Fatalf("verify parent hash chain: %v", err)
	}
	if err := log.Verify(context.Background(), "child-session-e2e"); err != nil {
		t.Fatalf("verify child hash chain: %v", err)
	}
	var childState session.Session
	if err := json.Unmarshal(childEvents[len(childEvents)-1].Payload, &childState); err != nil {
		t.Fatalf("decode child final state: %v", err)
	}
	if childState.ID != "child-session-e2e" || len(childState.Messages) != 1 || childState.Messages[0].Content != "fork source message" {
		t.Fatalf("child final state = %+v, want one rewound message", childState)
	}
	if childState.Mode != "plan" {
		t.Fatalf("child final mode = %q, want plan", childState.Mode)
	}
	parentSurface, err := log.Surface(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("replay parent surface: %v", err)
	}
	childSurface, err := log.Surface(context.Background(), "child-session-e2e")
	if err != nil {
		t.Fatalf("replay child surface: %v", err)
	}
	if !sessionEventSequenceContiguous(parentEvents) || !sessionEventSequenceContiguous(childEvents) {
		t.Fatal("fork/rewind event sequence is not contiguous")
	}
	for _, event := range childEvents {
		if event.Type == "session/rewind" {
			goto foundRewind
		}
	}
	t.Fatal("child history has no session/rewind marker")

foundRewind:
	receipt := map[string]any{
		"kind":                       "production-session-fork-rewind",
		"git_sha":                    productionGitSHA(t, repositoryRoot),
		"command":                    "CODE_AGENT_RUN_SESSION_E2E=1 go test ./tests/e2e -run TestProductionAgentForkAndRewindSurviveProcessBoundary -count=1",
		"source_session_id":          sessionID,
		"child_session_id":           "child-session-e2e",
		"parent_event_count":         len(parentEvents),
		"child_event_count":          len(childEvents),
		"schema_version":             childEvents[0].Version,
		"event_version":              childEvents[0].Version,
		"first_process_pid":          first.Process.Pid,
		"first_exit_code":            first.ProcessState.ExitCode(),
		"second_process_pid":         second.ProcessState.Pid(),
		"second_exit_code":           second.ProcessState.ExitCode(),
		"child_final_checksum":       childEvents[len(childEvents)-1].Checksum,
		"parent_sequence_contiguous": sessionEventSequenceContiguous(parentEvents),
		"child_sequence_contiguous":  sessionEventSequenceContiguous(childEvents),
		"parent_surface_event_count": len(parentSurface),
		"child_surface_event_count":  len(childSurface),
		"state_projection":           sessionStateReceipt(childState),
		"database_sha256":            productionFileSHA256(t, databasePath),
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode fork/rewind E2E receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "session-fork-rewind-process.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create fork/rewind receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write fork/rewind E2E receipt: %v", err)
	}
	t.Logf("fork/rewind E2E receipt written to ignored runtime path")
}

func sessionStateWithMessageCount(t *testing.T, events []session.Event, want int) int64 {
	t.Helper()
	for _, event := range events {
		if event.Type != "session/state" {
			continue
		}
		var state session.Session
		if err := json.Unmarshal(event.Payload, &state); err != nil {
			t.Fatalf("decode session state at seq %d: %v", event.Seq, err)
		}
		if len(state.Messages) == want {
			return event.Seq
		}
	}
	t.Fatalf("no session state with %d messages in %d events", want, len(events))
	return -1
}

func sessionEventSequenceContiguous(events []session.Event) bool {
	for index, event := range events {
		if event.Seq != int64(index) {
			return false
		}
	}
	return true
}

func sessionStateReceipt(state session.Session) map[string]any {
	return map[string]any{
		"message_count":       len(state.Messages),
		"plan_step_count":     len(state.Plan.Steps),
		"plan_mode":           state.Mode,
		"approved_tool_count": len(state.ApprovedTools),
		"approval_history":    len(state.ApprovalHistory),
		"undo_count":          len(state.Undo),
		"worktree_count":      len(state.Worktrees),
	}
}

func e2ERepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve E2E repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func buildProductionAgent(t *testing.T, repositoryRoot string) string {
	t.Helper()
	binaryName := "codeops-agent-session-e2e"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/agent")
	build.Dir = repositoryRoot
	if _, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build production agent (exit output omitted from receipt): %v", err)
	}
	return binaryPath
}

func productionGitSHA(t *testing.T, repositoryRoot string) string {
	t.Helper()
	output, err := exec.Command("git", "-C", repositoryRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("read production Git SHA: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func productionFileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production E2E artifact: %v", err)
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest[:])
}

func waitForSessionEvents(t *testing.T, databasePath string, minimum int) (string, int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		db, err := sql.Open("sqlite", databasePath)
		if err == nil {
			_, _ = db.Exec("PRAGMA busy_timeout = 100")
			rows, queryErr := db.Query(`SELECT session_id, COUNT(*) FROM session_events GROUP BY session_id`)
			if queryErr == nil {
				var id string
				var count int
				if rows.Next() && rows.Scan(&id, &count) == nil && count >= minimum {
					_ = rows.Close()
					_ = db.Close()
					return id, count
				}
				_ = rows.Close()
			}
			_ = db.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("session event log did not reach %d events before timeout", minimum)
	return "", 0
}

func waitForSessionState(t *testing.T, databasePath string, minimumMessages int, mode string) (string, int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		db, err := sql.Open("sqlite", databasePath)
		if err == nil {
			_, _ = db.Exec("PRAGMA busy_timeout = 100")
			rows, queryErr := db.Query(`SELECT session_id, COUNT(*) FROM session_events GROUP BY session_id`)
			if queryErr == nil {
				for rows.Next() {
					var id string
					var count int
					if rows.Scan(&id, &count) != nil {
						continue
					}
					var payload string
					stateErr := db.QueryRow(
						`SELECT payload FROM session_events WHERE session_id = ? AND type = 'session/state' ORDER BY seq DESC LIMIT 1`,
						id,
					).Scan(&payload)
					if stateErr != nil {
						continue
					}
					var state session.Session
					if json.Unmarshal([]byte(payload), &state) != nil {
						continue
					}
					if len(state.Messages) >= minimumMessages && state.Mode == mode {
						_ = rows.Close()
						_ = db.Close()
						return id, count
					}
				}
				_ = rows.Close()
			}
			_ = db.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("session event log did not reach %d messages in mode %q before timeout", minimumMessages, mode)
	return "", 0
}
