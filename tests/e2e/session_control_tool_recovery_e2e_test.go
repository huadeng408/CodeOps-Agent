package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/session"

	_ "modernc.org/sqlite"
)

func TestProductionSessionControlToolsRecoverAcrossProcessTermination(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_SESSION_CONTROL_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_SESSION_CONTROL_E2E=1 to run session control recovery E2E")
	}

	repositoryRoot := e2ERepositoryRoot(t)
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agent"), 0o755); err != nil {
		t.Fatalf("create E2E project: %v", err)
	}
	databasePath := filepath.Join(projectRoot, ".agent", "sessions", "sessions.sqlite")
	address := contextE2EAddress(t)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split orchestrator address: %v", err)
	}
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "session_control_server.py")
	settings, err := json.Marshal(map[string]any{
		"session_db_path":         databasePath,
		"memory_dir":              filepath.Join(projectRoot, ".agent", "memory"),
		"orchestrator_addr":       address,
		"orchestrator_auto_start": false,
		"mcp_config":              "missing-mcp.json",
		"sandbox":                 map[string]any{"enabled": false},
		"permissions": map[string]any{
			"allow": []map[string]string{
				{"tool": "SessionFork"},
				{"tool": "SessionRewind"},
			},
		},
	})
	if err != nil {
		t.Fatalf("encode settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agent", "settings.local.json"), settings, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	firstServer := startSessionControlServer(t, python, fixture, repositoryRoot, projectRoot, host, port, "first")
	waitForTCP(t, address, 20*time.Second, &strings.Builder{})
	agentBinary := buildProductionAgent(t, repositoryRoot)
	reader, writer := io.Pipe()
	firstAgent := exec.Command(agentBinary)
	firstAgent.Dir = projectRoot
	firstAgent.Stdin = reader
	var firstOutput strings.Builder
	firstAgent.Stdout = &firstOutput
	firstAgent.Stderr = &firstOutput
	if err := firstAgent.Start(); err != nil {
		t.Fatalf("start first production agent: %v", err)
	}
	if _, err := io.WriteString(writer, "SESSION_CONTROL_E2E\n"); err != nil {
		t.Fatalf("send first control request: %v", err)
	}
	parentID, parentEventCount := waitForSessionState(t, databasePath, 1, "chat")
	waitForSessionEventCount(t, databasePath, "child-session-tool-e2e", 5)
	if err := firstAgent.Process.Kill(); err != nil {
		t.Fatalf("terminate first production agent: %v", err)
	}
	_ = writer.Close()
	firstExit := firstAgent.Wait()
	if firstExit == nil {
		t.Fatal("first production agent exited cleanly; recovery was not exercised")
	}
	stopSessionControlServer(firstServer)

	secondServer := startSessionControlServer(t, python, fixture, repositoryRoot, projectRoot, host, port, "resume")
	waitForTCP(t, address, 20*time.Second, &strings.Builder{})
	secondAgent := exec.Command(agentBinary)
	secondAgent.Dir = projectRoot
	secondAgent.Stdin = strings.NewReader("/resume child-session-tool-e2e\nSESSION_CONTROL_E2E_RESUME\n")
	var secondOutput strings.Builder
	secondAgent.Stdout = &secondOutput
	secondAgent.Stderr = &secondOutput
	if err := secondAgent.Run(); err != nil {
		t.Fatalf("run recovery production agent: %v; output=%q", err, secondOutput.String())
	}
	if !strings.Contains(secondOutput.String(), "SESSION_CONTROL_RESUMED_OK") {
		t.Fatalf("recovery did not complete SessionRewind: %q", secondOutput.String())
	}
	stopSessionControlServer(secondServer)

	log, err := session.OpenSQLiteEventLog(databasePath)
	if err != nil {
		t.Fatalf("open recovered event log: %v", err)
	}
	defer log.Close()
	parentEvents, err := log.Events(context.Background(), parentID)
	if err != nil {
		t.Fatalf("read parent events: %v", err)
	}
	childEvents, err := log.Events(context.Background(), "child-session-tool-e2e")
	if err != nil {
		t.Fatalf("read child events: %v", err)
	}
	// The first agent may durably append the fork receipt after the initial
	// session/state poll returns and before the process is terminated. Recovery
	// must retain every event observed at the poll boundary, so an exact count
	// here would turn that valid in-flight append into a race.
	if len(parentEvents) < parentEventCount || len(childEvents) < 10 {
		t.Fatalf("recovered event counts parent=%d/%d child=%d", len(parentEvents), parentEventCount, len(childEvents))
	}
	if err := log.Verify(context.Background(), parentID); err != nil {
		t.Fatalf("verify parent chain: %v", err)
	}
	if err := log.Verify(context.Background(), "child-session-tool-e2e"); err != nil {
		t.Fatalf("verify child chain: %v", err)
	}
	var childState session.Session
	if err := json.Unmarshal(childEvents[len(childEvents)-1].Payload, &childState); err != nil {
		t.Fatalf("decode child state: %v", err)
	}
	if childState.ID != "child-session-tool-e2e" || len(childState.Messages) == 0 || childState.Messages[0].Content != "SESSION_CONTROL_E2E" {
		t.Fatalf("recovered child projection = %+v", childState)
	}
	if !sessionEventSequenceContiguous(parentEvents) || !sessionEventSequenceContiguous(childEvents) {
		t.Fatal("recovered session sequences are not contiguous")
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open recovered database: %v", err)
	}
	var sessionCount int
	if err := database.QueryRow("SELECT COUNT(DISTINCT session_id) FROM session_events").Scan(&sessionCount); err != nil {
		database.Close()
		t.Fatalf("count recovered sessions: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close recovered database: %v", err)
	}
	if sessionCount < 3 {
		t.Fatalf("recovered session count = %d, want parent, child, and second-process bootstrap", sessionCount)
	}

	receipt := map[string]any{
		"status":                    "VERIFIED",
		"kind":                      "production-session-control-recovery",
		"git_sha":                   productionGitSHA(t, repositoryRoot),
		"command":                   "CODE_AGENT_RUN_SESSION_CONTROL_E2E=1 go test ./tests/e2e -run TestProductionSessionControlToolsRecoverAcrossProcessTermination -count=1",
		"parent_session_id":         parentID,
		"child_session_id":          "child-session-tool-e2e",
		"parent_event_count":        len(parentEvents),
		"child_event_count":         len(childEvents),
		"schema_version":            childEvents[0].Version,
		"event_version":             childEvents[0].Version,
		"session_count":             sessionCount,
		"first_agent_pid":           firstAgent.Process.Pid,
		"first_agent_exit_code":     firstAgent.ProcessState.ExitCode(),
		"second_agent_pid":          secondAgent.ProcessState.Pid(),
		"second_agent_exit_code":    secondAgent.ProcessState.ExitCode(),
		"sequence_contiguous":       true,
		"parent_final_checksum":     parentEvents[len(parentEvents)-1].Checksum,
		"child_final_checksum":      childEvents[len(childEvents)-1].Checksum,
		"child_surface_event_count": len(mustSessionSurface(t, log, "child-session-tool-e2e")),
		"child_state_projection":    sessionStateReceipt(childState),
		"database_sha256":           productionFileSHA256(t, databasePath),
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode recovery receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "session-control-recovery-process.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create recovery receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write recovery receipt: %v", err)
	}
}

func startSessionControlServer(t *testing.T, python, fixture, repositoryRoot, projectRoot, host, port, stage string) *exec.Cmd {
	t.Helper()
	server := exec.Command(python, fixture, "--host", host, "--port", port, "--project-root", projectRoot, "--working-dir", projectRoot, "--memory-dir", filepath.Join(projectRoot, ".agent", "memory"))
	server.Dir = repositoryRoot
	server.Env = append(os.Environ(), "PYTHONPATH="+repositoryRoot, "CODE_AGENT_SESSION_CONTROL_STAGE="+stage)
	var output strings.Builder
	server.Stdout = &output
	server.Stderr = &output
	if err := server.Start(); err != nil {
		t.Fatalf("start %s session control server: %v", stage, err)
	}
	return server
}

func mustSessionSurface(t *testing.T, log *session.SQLiteEventLog, sessionID string) []session.Event {
	t.Helper()
	surface, err := log.Surface(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("read %s surface: %v", sessionID, err)
	}
	return surface
}

func stopSessionControlServer(server *exec.Cmd) {
	if server == nil || server.ProcessState != nil {
		return
	}
	if server.Process != nil {
		_ = server.Process.Kill()
	}
	_ = server.Wait()
}

func waitForSessionEventCount(t *testing.T, databasePath, sessionID string, minimum int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		database, err := sql.Open("sqlite", databasePath)
		if err == nil {
			var count int
			queryErr := database.QueryRow("SELECT COUNT(*) FROM session_events WHERE session_id = ?", sessionID).Scan(&count)
			_ = database.Close()
			if queryErr == nil && count >= minimum {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("session %s did not reach %d events", sessionID, minimum)
}
