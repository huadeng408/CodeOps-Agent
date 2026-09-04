package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/orchestrator"

	_ "modernc.org/sqlite"
)

// TestActorIdentityProcessE2E proves the production Go client -> gRPC ->
// Python runner path carries and audits the authenticated actor identity.
func TestActorIdentityProcessE2E(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_ACTOR_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_ACTOR_E2E=1 to run the actor identity process E2E")
	}

	repositoryRoot := contextE2ERepositoryRoot(t)
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "context_runtime_server.py")
	pythonPath := repositoryRoot
	if inherited := os.Getenv("PYTHONPATH"); inherited != "" {
		pythonPath += string(os.PathListSeparator) + inherited
	}
	t.Setenv("PYTHONPATH", pythonPath)
	t.Setenv("PYTHONUTF8", "1")
	projectRoot := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agent"), 0o755); err != nil {
		t.Fatalf("create project root: %v", err)
	}
	address := contextE2EAddress(t)
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:             address,
		AutoStart:           true,
		Command:             python,
		Args:                []string{fixture},
		ProjectRoot:         projectRoot,
		WorkingDir:          projectRoot,
		MemoryDir:           filepath.Join(projectRoot, ".agent", "memory"),
		StartupTimeout:      20 * time.Second,
		ConversationTimeout: 30 * time.Second,
	})
	t.Cleanup(manager.Stop)

	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("start Python orchestrator: %v", err)
	}
	response, err := client.ConverseWithHistory(
		context.Background(),
		"COMPACTION_CHECK",
		"actor-identity-process-e2e",
		nil,
	)
	if err != nil {
		t.Fatalf("production actor conversation: %v", err)
	}
	if !strings.Contains(response, "COMPACTION_OK:") {
		t.Fatalf("unexpected actor conversation response: %q", response)
	}

	databasePath := filepath.Join(projectRoot, ".agent", "context.sqlite")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open Python context database: %v", err)
	}
	defer database.Close()
	var payload string
	if err := database.QueryRow(
		"SELECT payload_json FROM context_events WHERE session_id = ? AND kind = 'actor/authorized' ORDER BY sequence LIMIT 1",
		"actor-identity-process-e2e",
	).Scan(&payload); err != nil {
		t.Fatalf("read actor audit event: %v", err)
	}
	var audit struct {
		ActorID       string `json:"actor_id"`
		SchemaVersion int    `json:"schema_version"`
	}
	if err := json.Unmarshal([]byte(payload), &audit); err != nil {
		t.Fatalf("decode actor audit event: %v", err)
	}
	if audit.ActorID != "actor:local" || audit.SchemaVersion != 1 {
		t.Fatalf("unexpected actor audit event: %+v", audit)
	}

	receipt := map[string]any{
		"status":               "VERIFIED",
		"git_sha":              contextE2EGitSHA(t, repositoryRoot),
		"run_id":               "actor-identity-process-e2e",
		"command":              "CODE_AGENT_RUN_ACTOR_E2E=1 go test ./tests/e2e -run TestActorIdentityProcessE2E -count=1",
		"go_to_python_grpc":    true,
		"actor_audit_event":    true,
		"actor_id":             audit.ActorID,
		"actor_schema_version": audit.SchemaVersion,
		"session_id":           "actor-identity-process-e2e",
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode actor receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "actor-identity-process.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create actor receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write actor receipt: %v", err)
	}
}
