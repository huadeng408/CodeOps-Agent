package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
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

func TestProductionSessionControlToolsCrossProcess(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_SESSION_CONTROL_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_SESSION_CONTROL_E2E=1 to run the session control tool E2E")
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
		"session_db_path":                           databasePath,
		"memory_dir":                                filepath.Join(projectRoot, ".agent", "memory"),
		"orchestrator_addr":                         address,
		"orchestrator_auto_start":                   false,
		"orchestrator_conversation_timeout_seconds": 30,
		"mcp_config":                                "missing-mcp.json",
		"sandbox":                                   map[string]any{"enabled": false},
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

	server := exec.Command(python, fixture, "--host", host, "--port", port, "--project-root", projectRoot, "--working-dir", projectRoot, "--memory-dir", filepath.Join(projectRoot, ".agent", "memory"))
	server.Dir = repositoryRoot
	server.Env = append(os.Environ(), "PYTHONPATH="+repositoryRoot)
	var serverOutput strings.Builder
	server.Stdout = &serverOutput
	server.Stderr = &serverOutput
	if err := server.Start(); err != nil {
		t.Fatalf("start session control Python server: %v", err)
	}
	t.Cleanup(func() {
		if server.Process != nil {
			_ = server.Process.Kill()
		}
		_ = server.Wait()
	})
	waitForTCP(t, address, 20*time.Second, &serverOutput)

	agentBinary := buildProductionAgent(t, repositoryRoot)
	agent := exec.Command(agentBinary)
	agent.Dir = projectRoot
	agent.Stdin = strings.NewReader("SESSION_CONTROL_E2E\n")
	var agentOutput strings.Builder
	agent.Stdout = &agentOutput
	agent.Stderr = &agentOutput
	if err := agent.Run(); err != nil {
		t.Fatalf("run production agent: %v; output=%q", err, agentOutput.String())
	}
	if !strings.Contains(agentOutput.String(), "SESSION_CONTROL_OK") {
		t.Fatalf("agent did not complete session controls: %q", agentOutput.String())
	}

	log, err := session.OpenSQLiteEventLog(databasePath)
	if err != nil {
		t.Fatalf("open session event log: %v", err)
	}
	defer log.Close()
	parentID, childEvents, parentEvents := readSessionControlHistories(t, log)
	if parentID == "" || len(parentEvents) < 3 {
		t.Fatalf("parent history = %q/%d, want original session history", parentID, len(parentEvents))
	}
	if len(childEvents) < 6 {
		t.Fatalf("child history = %d, want fork, rewind, and follow-up state events", len(childEvents))
	}
	if err := log.Verify(context.Background(), parentID); err != nil {
		t.Fatalf("verify parent hash chain: %v", err)
	}
	if err := log.Verify(context.Background(), "child-session-tool-e2e"); err != nil {
		t.Fatalf("verify child hash chain: %v", err)
	}
	var childState session.Session
	if err := json.Unmarshal(childEvents[len(childEvents)-1].Payload, &childState); err != nil {
		t.Fatalf("decode child final state: %v", err)
	}
	if childState.ID != "child-session-tool-e2e" || len(childState.Messages) == 0 || childState.Messages[0].Content != "SESSION_CONTROL_E2E" {
		t.Fatalf("unexpected child final projection: %+v", childState)
	}
	if !sessionEventSequenceContiguous(childEvents) || !sessionEventSequenceContiguous(parentEvents) {
		t.Fatal("session control event sequences are not contiguous")
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open receipt database: %v", err)
	}
	var sessionCount int
	if err := database.QueryRow("SELECT COUNT(DISTINCT session_id) FROM session_events").Scan(&sessionCount); err != nil {
		database.Close()
		t.Fatalf("count session histories: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close receipt database: %v", err)
	}
	if sessionCount != 2 {
		t.Fatalf("session history count = %d, want parent and child", sessionCount)
	}

	receipt := map[string]any{
		"status":               "VERIFIED",
		"kind":                 "production-session-control-tool",
		"git_sha":              productionGitSHA(t, repositoryRoot),
		"command":              "CODE_AGENT_RUN_SESSION_CONTROL_E2E=1 go test ./tests/e2e -run TestProductionSessionControlToolsCrossProcess -count=1",
		"parent_session_id":    parentID,
		"child_session_id":     "child-session-tool-e2e",
		"parent_event_count":   len(parentEvents),
		"child_event_count":    len(childEvents),
		"session_count":        sessionCount,
		"agent_exit_code":      agent.ProcessState.ExitCode(),
		"server_pid":           server.Process.Pid,
		"child_final_messages": len(childState.Messages),
		"database_sha256":      productionFileSHA256(t, databasePath),
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode session control receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "session-control-tool-process.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write session control receipt: %v", err)
	}
}

func readSessionControlHistories(t *testing.T, log *session.SQLiteEventLog) (string, []session.Event, []session.Event) {
	t.Helper()
	ids, err := log.SessionIDs(context.Background())
	if err != nil {
		t.Fatalf("list session histories: %v", err)
	}
	var parentID string
	var childEvents, parentEvents []session.Event
	for _, id := range ids {
		events, eventsErr := log.Events(context.Background(), id)
		if eventsErr != nil {
			t.Fatalf("read session %s: %v", id, eventsErr)
		}
		if id == "child-session-tool-e2e" {
			childEvents = events
			continue
		}
		if parentID == "" {
			parentID = id
			parentEvents = events
		}
	}
	return parentID, childEvents, parentEvents
}

func waitForTCP(t *testing.T, address string, timeout time.Duration, output *strings.Builder) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server %s did not start: %s", address, output.String())
}
