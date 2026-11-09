package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/session"
	"code-agent/internal/tools"

	_ "modernc.org/sqlite"
)

const contextE2ESessionID = "context-runtime-e2e"
const contextE2EMemorySessionID = "context-memory-runtime-e2e"

func TestLongTermMemoryRuntimeSurvivesOrchestratorRestart(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_CONTEXT_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_CONTEXT_E2E=1 to run the cross-language runtime E2E")
	}

	repositoryRoot := contextE2ERepositoryRoot(t)
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "context_runtime_server.py")
	projectRoot := t.TempDir()
	address := contextE2EAddress(t)
	pythonPath := repositoryRoot
	if inherited := os.Getenv("PYTHONPATH"); inherited != "" {
		pythonPath += string(os.PathListSeparator) + inherited
	}
	t.Setenv("PYTHONPATH", pythonPath)

	_, modelScope := contextE2EAdmittedSession(t, projectRoot)
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:             address,
		AutoStart:           true,
		Command:             python,
		Args:                []string{fixture},
		ProjectRoot:         projectRoot,
		WorkingDir:          projectRoot,
		MemoryDir:           filepath.Join(projectRoot, ".agent", "memory"),
		StartupTimeout:      15 * time.Second,
		ConversationTimeout: 30 * time.Second,
		ModelScopeFactory:   modelScope,
	})
	t.Cleanup(manager.Stop)

	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("start real Python orchestrator: %v", err)
	}
	first, err := client.ConverseWithHistory(
		context.Background(),
		"PERSIST_MEMORY_ANCHOR",
		contextE2EMemorySessionID,
		nil,
	)
	if err != nil {
		t.Fatalf("persist reflection conversation: %v", err)
	}
	firstProcessID := contextE2EProcessID(t, first, "MEMORY_ANCHOR_WRITTEN:")
	noise, err := client.ConverseWithHistory(
		context.Background(),
		"PERSIST_MEMORY_NOISE",
		contextE2EMemorySessionID,
		nil,
	)
	if err != nil {
		t.Fatalf("persist noise reflection conversation: %v", err)
	}
	noiseProcessID := contextE2EProcessID(t, noise, "MEMORY_NOISE_WRITTEN:")

	restartedClient, err := manager.Restart(context.Background())
	if err != nil {
		t.Fatalf("restart real Python orchestrator: %v", err)
	}
	second, err := restartedClient.ConverseWithHistory(
		context.Background(),
		"SEARCH MEMORY ANCHOR AFTER RESTART",
		contextE2EMemorySessionID,
		nil,
	)
	if err != nil {
		t.Fatalf("search reflection after restart: %v", err)
	}
	secondProcessID := contextE2EProcessID(t, second, "MEMORY_RECOVERED:")
	if secondProcessID == firstProcessID {
		t.Fatalf("orchestrator process did not change across restart: pid=%d", firstProcessID)
	}

	database, err := sql.Open("sqlite", filepath.Join(projectRoot, ".agent", "context.sqlite"))
	if err != nil {
		t.Fatalf("open context SQLite: %v", err)
	}
	defer database.Close()
	var content string
	if err := database.QueryRow(
		"SELECT content FROM long_term_memory WHERE session_id = ? AND content LIKE ? ORDER BY created_at ASC LIMIT 1",
		contextE2EMemorySessionID,
		"%MEMORY_ANCHOR_WRITTEN%",
	).Scan(&content); err != nil {
		t.Fatalf("read persisted reflection: %v", err)
	}
	if !strings.Contains(content, "MEMORY_ANCHOR_WRITTEN") || !strings.Contains(content, "turn 1") {
		t.Fatalf("persisted reflection = %q, want searchable outcome and turn metadata", content)
	}
	if strings.Contains(strings.ToLower(content), "api_key") {
		t.Fatalf("persisted reflection contains credential-shaped text: %q", content)
	}
	contextE2EAssertMemorySQLite(t, filepath.Join(projectRoot, ".agent", "context.sqlite"))
	var schemaVersion int
	if err := database.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil {
		t.Fatalf("read context schema version: %v", err)
	}
	receipt := map[string]any{
		"status":                 "VERIFIED",
		"git_sha":                contextE2EGitSHA(t, repositoryRoot),
		"session_id":             contextE2EMemorySessionID,
		"initial_response_pid":   firstProcessID,
		"noise_response_pid":     noiseProcessID,
		"recovery_response_pid":  secondProcessID,
		"orchestrator_restarted": true,
		"sqlite_user_version":    schemaVersion,
		"reflection_search":      true,
		"sensitive_content":      false,
	}
	receiptJSON, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatalf("encode long-term memory receipt: %v", err)
	}
	receiptPath := filepath.Join(repositoryRoot, ".runtime", "e2e", "context-memory-process-recovery.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatalf("create long-term memory receipt directory: %v", err)
	}
	if err := os.WriteFile(receiptPath, append(receiptJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write long-term memory receipt: %v", err)
	}
}

func TestContextRuntimeSurvivesOrchestratorRestart(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_CONTEXT_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_CONTEXT_E2E=1 to run the cross-language runtime E2E")
	}

	repositoryRoot := contextE2ERepositoryRoot(t)
	python := contextE2EPython(t)
	fixture := filepath.Join(repositoryRoot, "tests", "e2e", "context_runtime_server.py")
	projectRoot := t.TempDir()
	address := contextE2EAddress(t)

	pythonPath := repositoryRoot
	if inherited := os.Getenv("PYTHONPATH"); inherited != "" {
		pythonPath += string(os.PathListSeparator) + inherited
	}
	t.Setenv("PYTHONPATH", pythonPath)

	_, modelScope := contextE2EAdmittedSession(t, projectRoot)
	manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
		Address:             address,
		AutoStart:           true,
		Command:             python,
		Args:                []string{fixture},
		ProjectRoot:         projectRoot,
		WorkingDir:          projectRoot,
		MemoryDir:           filepath.Join(projectRoot, ".agent", "memory"),
		StartupTimeout:      15 * time.Second,
		ConversationTimeout: 30 * time.Second,
		ModelScopeFactory:   modelScope,
	})
	t.Cleanup(manager.Stop)

	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("start real Python orchestrator: %v", err)
	}

	executor := tools.NewExecutor(projectRoot)
	var toolCalls atomic.Int32
	handler := contextE2EToolHandler(executor, &toolCalls)
	response, err := client.ConverseWithHistory(
		context.Background(),
		"CREATE_CONTEXT_RECOVERY_MARKER",
		contextE2ESessionID,
		nil,
		handler,
	)
	if err != nil {
		t.Fatalf("initial conversation: %v", err)
	}
	firstProcessID := contextE2EProcessID(t, response, "WRITE_COMPLETE:")

	markerPath := filepath.Join(projectRoot, "runtime", "context-recovery.txt")
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read file written by Go tool executor: %v", err)
	}
	if string(marker) != "context recovery marker\n" {
		t.Fatalf("marker content = %q", marker)
	}
	if toolCalls.Load() != 1 {
		t.Fatalf("tool calls before restart = %d, want 1", toolCalls.Load())
	}

	restartedClient, err := manager.Restart(context.Background())
	if err != nil {
		t.Fatalf("restart real Python orchestrator: %v", err)
	}
	recovery, err := restartedClient.ConverseWithHistory(
		context.Background(),
		"RESUME_AFTER_RESTART",
		contextE2ESessionID,
		nil,
		handler,
	)
	if err != nil {
		t.Fatalf("recovery conversation: %v", err)
	}
	restartedProcessID := contextE2EProcessID(t, recovery, "RECOVERY_CONFIRMED:")
	if restartedProcessID == firstProcessID {
		t.Fatalf("orchestrator process did not change across restart: pid=%d", firstProcessID)
	}
	if toolCalls.Load() != 1 {
		t.Fatalf("recovery unexpectedly executed another tool; calls = %d", toolCalls.Load())
	}
	markerAfterRestart, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read tool-created file after orchestrator restart: %v", err)
	}
	if string(markerAfterRestart) != "context recovery marker\n" {
		t.Fatalf("marker content after restart = %q", markerAfterRestart)
	}

	contextE2EAssertSQLite(t, filepath.Join(projectRoot, ".agent", "context.sqlite"))
}

// The upstream is deterministic; canonical history, model admission and persistence are real.
func TestCanonicalCompactionRuntimeAcrossRestart(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_CONTEXT_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_CONTEXT_E2E=1 to run the cross-language runtime E2E")
	}
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprintf("manual=%t", manual), func(t *testing.T) {
			ctx := context.Background()
			repo, project := contextE2ERepositoryRoot(t), t.TempDir()
			t.Setenv("PYTHONPATH", repo)
			t.Setenv("CODE_AGENT_E2E_PROVIDER", "deterministic")
			providerAddress := contextE2EAddress(t)
			t.Setenv("CODE_AGENT_CONTEXT_E2E_PROVIDER_ADDRESS", providerAddress)
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(project, "sessions.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ledger.Close() })
			gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{
				Protocol: "openai", BaseURL: "http://" + providerAddress + "/v1",
				Model: "deterministic-context-e2e", APIKey: "fixture-only",
				InputLimit: 100_000, OutputLimit: 4096,
			})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(gateway.Close)
			manager := orchestrator.NewProcessManager(orchestrator.ProcessConfig{
				Address: contextE2EAddress(t), AutoStart: true,
				Command:     contextE2EPython(t),
				Args:        []string{filepath.Join(repo, "tests", "e2e", "context_runtime_server.py"), "--context-window", "512"},
				ProjectRoot: project, WorkingDir: project, MemoryDir: filepath.Join(project, ".agent", "memory"),
				StartupTimeout: 15 * time.Second, ConversationTimeout: 30 * time.Second,
				ModelScopeFactory: gateway.BindActor,
			})
			t.Cleanup(manager.Stop)
			workbench := session.NewWorkbench(ledger, nil)
			created, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "canonical compaction", "", project)
			if err != nil {
				t.Fatal(err)
			}
			actor, err := identity.Default().BindSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			for index := 0; index < 24; index++ {
				text := fmt.Sprintf("historical context %02d %s", index, strings.Repeat("h", 160))
				if index == 0 {
					text = "Standing constraint: 禁止网络访问，不增依赖 (no network, no new deps). " + text
				}
				if _, err := workbench.AppendUserMessage(ctx, 7, created.ID, int64(index+1), text); err != nil {
					t.Fatal(err)
				}
			}
			original, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			client, err := manager.Client(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var adapter session.ConversationAdapter = client
			if manual {
				adapter = manualCompactionConversation{client}
			}
			runner := session.NewSessionRunner(workbench, adapter, nil, session.SessionRunnerOptions{WorkerID: "compact-first"})
			t.Cleanup(func() { _ = runner.Close() })
			contextE2ERun(t, ledger, runner, actor, "compact-first", "COMPACTION_CHECK")
			surface, err := ledger.Surface(ctx, created.ID)
			if err != nil || len(surface) == 0 || surface[0].Type != "context/compaction" {
				t.Fatalf("canonical compaction missing: surface=%d err=%v", len(surface), err)
			}
			var summary struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(surface[0].Payload, &summary); err != nil ||
				!strings.Contains(summary.Summary, "Historical context retained for the next agent turn.") {
				t.Fatal("expected model summary, not a silent deterministic fallback")
			}
			if !strings.Contains(summary.Summary, "禁止网络访问，不增依赖") {
				t.Fatalf("canonical compaction dropped standing constraint: %q", summary.Summary)
			}
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CODE_AGENT_CONTEXT_E2E_WINDOW", "8192")
			restarted, err := manager.Restart(ctx)
			if err != nil {
				t.Fatal(err)
			}
			recovery := session.NewSessionRunner(workbench, restarted, nil, session.SessionRunnerOptions{WorkerID: "compact-recovered"})
			t.Cleanup(func() { _ = recovery.Close() })
			contextE2ERun(t, ledger, recovery, actor, "compact-recovered", "COMPACTION_RECOVER")
			if err := recovery.Close(); err != nil {
				t.Fatal(err)
			}
			final, err := session.ReadVerifiedSnapshot(ctx, ledger, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			for index, event := range original.Events {
				if final.Events[index].EventID != event.EventID || final.Events[index].Checksum != event.Checksum {
					t.Fatalf("original event changed: %d", index)
				}
			}
			recovered := false
			for _, event := range final.Events {
				if event.Type == "assistant/message" && strings.Contains(string(event.Payload), "COMPACTION_RECOVERED:") {
					recovered = true
				}
			}
			if !recovered {
				t.Fatal("restarted Python did not receive the persisted canonical summary")
			}
			t.Logf("canonical Session %s: %d original events preserved, %d final events", created.ID, len(original.Events), len(final.Events))
		})
	}
}

type manualCompactionConversation struct{ client *orchestrator.Client }

func (a manualCompactionConversation) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	update, err := a.client.CompactConversation(ctx, request, handlers)
	if err != nil {
		return orchestrator.ConversationResult{}, err
	}
	if update == nil || update.GetTrigger() != "manual" || update.GetRemovedMessages() == 0 {
		return orchestrator.ConversationResult{}, errors.New("manual compaction returned no update")
	}
	return orchestrator.ConversationResult{Success: true, Message: "Manual compaction committed"}, nil
}

func contextE2ERun(t *testing.T, ledger session.EventLog, runner *session.SessionRunner, actor identity.Actor, requestID, content string) {
	t.Helper()
	ctx := context.Background()
	events, err := ledger.Events(ctx, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{
		RequestID: requestID, SessionID: actor.SessionID, OwnerID: 7, Actor: actor,
		ExpectedSeq: int64(len(events)), Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		current, err := runner.Run(ctx, actor.SessionID, run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == session.RunCompleted {
			return
		}
		if current.Status == session.RunFailed || current.Status == session.RunCanceled {
			t.Fatalf("canonical run ended unsuccessfully: %+v", current)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("canonical run did not finish within 30 seconds")
}

// Only the upstream model is a double; Gateway, budget and Session Ledger are real.
func contextE2EAdmittedSession(t *testing.T, projectRoot string) (*session.Manager, orchestrator.ModelScopeFactory) {
	t.Helper()
	store := session.NewSQLiteEventStore(filepath.Join(projectRoot, ".agent", "session.sqlite"))
	manager := session.NewManager(store)
	t.Cleanup(func() { _ = manager.Close() })
	current := manager.NewSession(projectRoot)
	actor, err := identity.Default().BindSession(current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bound := manager.SetActor(actor); bound.Actor.Validate() != nil {
		t.Fatal("persist fixture Session actor")
	}
	ledger, err := store.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	providerAddress := contextE2EAddress(t)
	t.Setenv("CODE_AGENT_E2E_PROVIDER", "deterministic")
	t.Setenv("CODE_AGENT_CONTEXT_E2E_PROVIDER_ADDRESS", providerAddress)
	gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{
		Protocol: "openai", BaseURL: "http://" + providerAddress + "/v1",
		Model: "deterministic-context-e2e", APIKey: "fixture-only",
		InputLimit: 100_000, OutputLimit: 4096,
	})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
	return manager, func(actor identity.Actor, purpose string) (*codeagentpb.ModelGatewayBinding, func(), error) {
		binding, release, err := gateway.BindActor(actor, purpose)
		if err == nil {
			return binding, release, nil
		}
		return gateway.Bind(admission.ActorOwner(actor), current.ID, current.ID)
	}
}

func contextE2EProcessID(t *testing.T, response, prefix string) int {
	t.Helper()
	raw, found := strings.CutPrefix(response, prefix)
	if !found {
		t.Fatalf("response = %q, want prefix %q", response, prefix)
	}
	processID, err := strconv.Atoi(raw)
	if err != nil || processID <= 0 {
		t.Fatalf("response process ID = %q, want positive integer", raw)
	}
	return processID
}

func contextE2EToolHandler(executor *tools.Executor, calls *atomic.Int32) orchestrator.ToolHandler {
	return func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
		calls.Add(1)
		arguments := map[string]any{}
		if err := json.Unmarshal([]byte(call.ParametersJSON), &arguments); err != nil {
			return orchestrator.ToolResult{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Error:      fmt.Sprintf("decode tool parameters: %v", err),
				ExitCode:   1,
			}
		}
		result, executeErr := executor.Execute(ctx, tools.ToolRequest{
			Name:      call.Name,
			Arguments: arguments,
		})
		converted := orchestrator.ToolResult{
			ToolCallID: call.ID,
			ToolName:   result.Name,
			Output:     result.Output,
			Error:      result.Error,
			ExitCode:   int32(result.ExitCode),
			Truncated:  result.Truncated,
		}
		if result.Spill != nil {
			converted.Spill = &orchestrator.SpillRef{
				Locator: result.Spill.Locator,
				SHA256:  result.Spill.SHA256,
				Bytes:   result.Spill.Bytes,
			}
		}
		if executeErr != nil && converted.Error == "" {
			converted.Error = executeErr.Error()
			converted.ExitCode = 1
		}
		return converted
	}
}

func contextE2EAssertSQLite(t *testing.T, databasePath string) {
	contextE2EAssertSQLiteSession(
		t,
		databasePath,
		contextE2ESessionID,
		[]string{"tool_call", "file_diff", "reflection"},
	)
}

func contextE2EAssertMemorySQLite(t *testing.T, databasePath string) {
	contextE2EAssertSQLiteSession(t, databasePath, contextE2EMemorySessionID, []string{"reflection"})
}

func contextE2EAssertSQLiteSession(t *testing.T, databasePath, sessionID string, kinds []string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open context SQLite: %v", err)
	}
	defer database.Close()

	var integrity string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("check context SQLite integrity: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("context SQLite integrity = %q", integrity)
	}

	for _, kind := range kinds {
		var count int
		err := database.QueryRow(
			"SELECT COUNT(*) FROM context_events WHERE session_id = ? AND kind = ?",
			sessionID,
			kind,
		).Scan(&count)
		if err != nil {
			t.Fatalf("count %s events: %v", kind, err)
		}
		if count == 0 {
			t.Fatalf("no persisted %s event survived restart for %s", kind, sessionID)
		}
	}
}

func contextE2ERepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func contextE2EPython(t *testing.T) string {
	t.Helper()
	candidates := []string{os.Getenv("PYTHON_EXECUTABLE"), "python", "python3"}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
	}
	t.Fatal("Python executable not found")
	return ""
}

func contextE2EGitSHA(t *testing.T, repositoryRoot string) string {
	t.Helper()
	command := exec.Command("git", "-C", repositoryRoot, "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("resolve current git SHA: %v", err)
	}
	sha := strings.TrimSpace(string(output))
	if len(sha) < 12 {
		t.Fatalf("git SHA is unexpectedly short: %q", sha)
	}
	return sha
}

func contextE2EAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate orchestrator port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release orchestrator port: %v", err)
	}
	return address
}
