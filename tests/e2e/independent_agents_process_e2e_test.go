package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/mcp"
	"code-agent/internal/memory"
	"code-agent/internal/session"
	"code-agent/internal/telemetry/genai"

	"go.opentelemetry.io/otel/propagation"
)

const (
	independentAgentMCPEchoMarker             = "MCP_E2E_ECHO_OK"
	independentAgentSandboxMarker             = "SANDBOX_READ_SENTINEL"
	independentAgentSandboxWriteBlockedMarker = "SANDBOX_WRITE_BLOCKED"
	independentAgentSandboxWriteFailedMarker  = "SANDBOX_WRITE_UNEXPECTED"
)

func TestProductionIndependentAgentsAndMemoryProcess(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_INDEPENDENT_AGENTS_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_INDEPENDENT_AGENTS_E2E=1 for the fixture-backed process integration")
	}
	repo := e2ERepositoryRoot(t)
	runBase := filepath.Join(repo, ".runtime", "e2e", "independent-agents-runs")
	if err := os.MkdirAll(runBase, 0700); err != nil {
		t.Fatal(err)
	}
	runRoot, err := os.MkdirTemp(runBase, "run-")
	if err != nil {
		t.Fatal(err)
	}
	runID := filepath.Base(runRoot)
	if requested := strings.TrimSpace(os.Getenv("CODE_AGENT_INDEPENDENT_AGENTS_E2E_RUN_ID")); requested != "" {
		if filepath.Base(requested) != requested || requested == "." || requested == ".." {
			t.Fatal("CODE_AGENT_INDEPENDENT_AGENTS_E2E_RUN_ID must be one safe name")
		}
		runID = requested
	}
	providerKind := strings.ToLower(strings.TrimSpace(os.Getenv("CODE_AGENT_E2E_PROVIDER")))
	providerMode := providerKind == "openai" || providerKind == "anthropic"
	if providerMode {
		startIndependentAgentEvalTrace(t, runID)
	}
	checks := map[string]bool{
		"production_go_cli": false, "independent_child_session": false, "child_context_isolated": false,
		"input_required_followup": false, "skill_lazy_loading": false, "file_artifact_pinned": false,
		"two_go_processes": false, "two_python_processes": false, "same_task_after_restart": false,
		"child_history_after_restart": false, "reflection_without_tools": false,
		"typed_memory_recovered": false, "ledger_hash_chains": false,
	}
	releaseChecks := map[string]bool{
		"agent_card": false, "message_text_file_json": false, "task_lifecycle": false,
		"isolated_child_context": false, "skill_lazy_loaded": false, "harness_authorized_tool": false,
		"mcp_call": false, "sandbox_enforced": false, "artifact_pinned": false,
		"memory_reflection_written": false, "restart_recall": false, "ledger_hash_chain": false,
	}
	evidenceType := "fixture-backed-production-cli-grpc"
	if providerMode {
		evidenceType = "provider-backed-production-cli-grpc"
	}
	receipt := map[string]any{"status": "IMPLEMENTED", "evidence_type": evidenceType,
		"provider_backed": false, "provider_kind": providerKind, "provider_call_count": 0,
		"git_sha": contextE2EGitSHA(t, repo), "run_id": runID,
		"command": "CODE_AGENT_RUN_INDEPENDENT_AGENTS_E2E=1 go test ./tests/e2e -run TestProductionIndependentAgentsAndMemoryProcess -count=1",
		"checks":  checks, "release_checks": releaseChecks, "artifacts": runRoot}
	parentID, childID := "", ""
	defer func() {
		passed := 0
		for _, ok := range checks {
			if ok {
				passed++
			}
		}
		receipt["passed"] = passed
		receipt["total"] = len(checks)
		releasePassed := 0
		for _, ok := range releaseChecks {
			if ok {
				releasePassed++
			}
		}
		receipt["release_passed"] = releasePassed
		receipt["release_total"] = len(releaseChecks)
		receipt["exit_code"] = 0
		if t.Failed() {
			receipt["exit_code"] = 1
		}
		status, _ := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
		receipt["source_dirty"] = len(strings.TrimSpace(string(status))) > 0
		var artifacts []map[string]string
		_ = filepath.WalkDir(runRoot, func(path string, item os.DirEntry, walkErr error) error {
			if walkErr != nil || item.IsDir() || filepath.Base(path) == "receipt.json" {
				return walkErr
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			digest := sha256.Sum256(body)
			artifacts = append(artifacts, map[string]string{"path": path, "sha256": fmt.Sprintf("%x", digest)})
			return nil
		})
		receipt["artifact_manifest"] = artifacts
		if routePath := strings.TrimSpace(os.Getenv("CODE_AGENT_INDEPENDENT_AGENTS_E2E_PROVIDER_ROUTE_RECEIPT")); routePath != "" {
			if routeBody, readErr := os.ReadFile(routePath); readErr == nil {
				routeDigest := sha256.Sum256(routeBody)
				receipt["provider_route_sha256"] = fmt.Sprintf("%x", routeDigest)
				count, valid := independentAgentProviderRoute(routeBody, runID, providerKind, parentID, childID)
				receipt["provider_call_count"] = count
				receipt["provider_backed"] = providerMode && valid
			}
		}
		body, _ := json.MarshalIndent(receipt, "", "  ")
		receiptPaths := []string{filepath.Join(runRoot, "receipt.json"), filepath.Join(repo, ".runtime", "e2e", "independent-agents-process.json")}
		if requested := strings.TrimSpace(os.Getenv("CODE_AGENT_INDEPENDENT_AGENTS_E2E_RECEIPT")); requested != "" {
			receiptPaths = []string{requested}
		}
		for _, path := range receiptPaths {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Errorf("create receipt directory: %v", err)
				continue
			}
			if err := os.WriteFile(path, append(body, '\n'), 0600); err != nil {
				t.Errorf("persist receipt: %v", err)
			}
		}
	}()
	project := filepath.Join(runRoot, "project")
	skillDir := filepath.Join(project, ".agents", "skills", "case-inspect")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, value []byte) {
		t.Helper()
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: case-inspect\ndescription: Inspect explicit sources\nallowed-tools: Read\n---\nSKILL_BODY_LAZY_MARKER\n"))
	write(filepath.Join(project, "report.txt"), []byte("independent source report\n"))
	write(filepath.Join(project, "sandbox-sentinel.txt"), []byte(independentAgentSandboxMarker+"\n"))
	if err := os.MkdirAll(filepath.Join(project, ".agent"), 0700); err != nil {
		t.Fatal(err)
	}
	address := contextE2EAddress(t)
	_, port, _ := net.SplitHostPort(address)
	dbPath := filepath.Join(project, ".agent", "sessions.sqlite")
	mcpConfig := "missing-mcp.json"
	sandboxSettings := map[string]any{"enabled": false}
	pythonExecutable := contextE2EPython(t)
	if providerMode {
		backend := strings.ToLower(strings.TrimSpace(os.Getenv("CODE_AGENT_E2E_SANDBOX_BACKEND")))
		image := strings.TrimSpace(os.Getenv("CODE_AGENT_E2E_SANDBOX_IMAGE"))
		wslDistro := strings.TrimSpace(os.Getenv("CODE_AGENT_E2E_WSL_DISTRO"))
		if (backend != "docker" && backend != "wsl2") || image == "" || (backend == "wsl2" && wslDistro == "") {
			t.Fatal("provider lane requires an explicit docker or wsl2 sandbox, image, and wsl2 distro when applicable")
		}
		receipt["sandbox_backend"] = backend
		mcpConfig = ".mcp.json"
		sandboxSettings = map[string]any{
			"enabled": true, "backend": backend, "image": image, "wsl_distro": wslDistro,
			"trust_root": project, "allow_workspace_write": false,
		}
		mcpBody, marshalErr := json.Marshal(map[string]any{"servers": map[string]any{"e2e": map[string]any{
			"command":     pythonExecutable,
			"args":        []string{filepath.Join(repo, "tests", "e2e", "independent_agents_runtime_server.py"), "--mcp-stdio"},
			"working_dir": ".",
		}}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		write(filepath.Join(project, mcpConfig), mcpBody)
	}
	settings, _ := json.Marshal(map[string]any{"session_db_path": dbPath, "memory_dir": filepath.Join(project, ".agent", "legacy-memory"),
		"orchestrator_addr": address, "orchestrator_auto_start": false, "orchestrator_conversation_timeout_seconds": 30,
		"mcp_config": mcpConfig, "sandbox": sandboxSettings, "worktree_base_ref": "HEAD",
		"permissions": map[string]any{"allow": []map[string]string{{"tool": "SpawnAgent"}, {"tool": "AgentTask"}, {"tool": "PublishArtifact"}, {"tool": "Skill"}, {"tool": "RecallMemory"}, {"tool": "Read"}, {"tool": "e2e_echo"}, {"tool": "Bash"}}}})
	write(filepath.Join(project, ".agent", "settings.local.json"), settings)
	if providerMode {
		write(filepath.Join(project, ".gitignore"), []byte(".agent/sessions.sqlite*\n.agent/worktrees/\n.agent/legacy-memory/\n.agent/fixture-memory/\n"))
		runGit := func(arguments ...string) {
			t.Helper()
			cmd := exec.Command("git", append([]string{"-C", project}, arguments...)...)
			if output, gitErr := cmd.CombinedOutput(); gitErr != nil {
				t.Fatalf("git %s failed: %v: %s", strings.Join(arguments, " "), gitErr, strings.TrimSpace(string(output)))
			}
		}
		runGit("init")
		runGit("config", "user.name", "CodeOps Agent E2E")
		runGit("config", "user.email", "codeops-agent-e2e@example.invalid")
		runGit("add", "--all")
		runGit("commit", "-m", "independent agent e2e fixture")
	}
	env := independentAgentProcessEnv(repo, providerKind)
	env = append(env, "PYTHONPATH="+repo)
	pythonPIDs, goPIDs, goExitCodes := []int{}, []int{}, []int{}
	receipt["python_pids"], receipt["go_pids"], receipt["go_exit_codes"] = pythonPIDs, goPIDs, goExitCodes
	startServer := func(stage int) func() {
		t.Helper()
		logPath := filepath.Join(runRoot, fmt.Sprintf("python-%d.log", stage))
		stopPath := filepath.Join(runRoot, fmt.Sprintf("python-%d.stop", stage))
		_ = os.Remove(stopPath)
		log, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(pythonExecutable, filepath.Join(repo, "tests", "e2e", "independent_agents_runtime_server.py"), "--port", port, "--project-root", project,
			"--observations", filepath.Join(runRoot, fmt.Sprintf("observations-%d.jsonl", stage)), "--stop-file", stopPath)
		cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = project, env, log, log
		if err := cmd.Start(); err != nil {
			_ = log.Close()
			t.Fatal(err)
		}
		pythonPIDs = append(pythonPIDs, cmd.Process.Pid)
		receipt["python_pids"] = pythonPIDs
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
			_ = log.Close()
		}()
		stopped := false
		stop := func() {
			if !stopped {
				stopped = true
				_ = os.WriteFile(stopPath, []byte("stop\n"), 0600)
				select {
				case <-done:
				case <-time.After(15 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
			}
		}
		t.Cleanup(stop)
		waitForTCP(t, address, 20*time.Second, &strings.Builder{})
		return stop
	}
	binary := filepath.Join(runRoot, "code-agent")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/agent")
	build.Dir = repo
	if output, err := build.CombinedOutput(); err != nil {
		write(filepath.Join(runRoot, "build.log"), output)
		t.Fatalf("production agent build failed: %v", err)
	}
	runCLI := func(input string, stage int) {
		t.Helper()
		timeout := 60 * time.Second
		if providerMode {
			timeout = 180 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary)
		cmd.Dir, cmd.Env, cmd.Stdin = project, env, strings.NewReader(input)
		output, err := cmd.CombinedOutput()
		write(filepath.Join(runRoot, fmt.Sprintf("go-%d.log", stage)), output)
		goPIDs = append(goPIDs, cmd.ProcessState.Pid())
		goExitCodes = append(goExitCodes, cmd.ProcessState.ExitCode())
		receipt["go_pids"], receipt["go_exit_codes"] = goPIDs, goExitCodes
		if err != nil || !strings.Contains(string(output), "PARENT_DELEGATION_COMPLETED") {
			t.Fatalf("production CLI stage %d did not finish: exit=%d error=%v; log=%s", stage, cmd.ProcessState.ExitCode(), err, filepath.Join(runRoot, fmt.Sprintf("go-%d.log", stage)))
		}
	}
	stopFirst := startServer(1)
	firstInput := "DELEGATE_AGENT_CASE PARENT_PRIVATE_HISTORY\n"
	if providerMode {
		firstInput = `PARENT_PRIVATE_HISTORY. Run the independent-agent acceptance exactly as follows. Use SpawnAgent with kind general and allowed_tools exactly ["Read","Skill","RecallMemory","e2e_echo","Bash"]. The explicit assignment must contain the text EXPLICIT_CHILD_MATERIAL, the file report.txt, and JSON {"scope":"source"}. Instruct the child to first call e2e_echo with text MCP_E2E_ECHO_OK, then call Bash with the exact command "test -f sandbox-sentinel.txt && cat sandbox-sentinel.txt && if printf probe > sandbox-write-probe.txt 2>/dev/null; then echo SANDBOX_WRITE_UNEXPECTED; else echo SANDBOX_WRITE_BLOCKED; fi" and require output containing both SANDBOX_READ_SENTINEL and SANDBOX_WRITE_BLOCKED. It must then ask which branch to inspect, after the answer "main" load Skill case-inspect, publish report.txt plus JSON as an artifact, and finish. Answer the child's input request through AgentTask, wait for completion, then reply with exactly PARENT_DELEGATION_COMPLETED.` + "\n"
	}
	runCLI(firstInput, 1)
	checks["production_go_cli"] = true
	ledger, err := session.OpenSQLiteEventLog(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := ledger.SessionIDs(context.Background())
	for _, id := range ids {
		events, _ := ledger.Events(context.Background(), id)
		for _, event := range events {
			if event.Type == "agent/task-linked" {
				var link struct {
					Child string `json:"child_session_id"`
				}
				if json.Unmarshal(event.Payload, &link) != nil {
					t.Fatal("invalid task link")
				}
				parentID, childID = id, link.Child
			}
		}
	}
	_ = ledger.Close()
	checks["independent_child_session"] = parentID != "" && childID != "" && parentID != childID
	receipt["parent_session_id"], receipt["child_session_id"] = parentID, childID
	stopFirst()
	stopSecond := startServer(2)
	secondInput := "RESUME_AGENT_CASE"
	if providerMode {
		secondInput = `RESUME_AGENT_CASE. After this process restart, use AgentTask list to recover the existing child. Send that same child the text "Continue after process restart". The child must call RecallMemory for its earlier independent-agent history, load Skill case-inspect again, publish a new pinned artifact, and complete. Wait for it and reply with exactly PARENT_DELEGATION_COMPLETED.`
	}
	runCLI("/resume "+parentID+"\n"+secondInput+"\n", 2)
	stopSecond()
	checks["two_go_processes"] = len(goPIDs) == 2 && goPIDs[0] != goPIDs[1]
	checks["two_python_processes"] = len(pythonPIDs) == 2 && pythonPIDs[0] != pythonPIDs[1]
	childContextObserved, childContextAllIsolated := false, true
	skillMetadataObserved, skillMetadataOnly, skillBodyLoaded := false, true, false
	reflectionObserved, reflectionWithoutTools := false, true
	memoryRecalled := false
	for stage := 1; stage <= 2; stage++ {
		body, err := os.ReadFile(filepath.Join(runRoot, fmt.Sprintf("observations-%d.jsonl", stage)))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			var observation map[string]any
			if json.Unmarshal([]byte(line), &observation) != nil {
				t.Fatal("invalid observation")
			}
			for source, target := range map[string]string{"task_input_required": "input_required_followup",
				"file_artifact_pinned": "file_artifact_pinned", "restart_history": "child_history_after_restart"} {
				if observation[source] == true {
					checks[target] = true
				}
			}
			if value, ok := observation["child_context_isolated"].(bool); ok {
				childContextObserved = true
				childContextAllIsolated = childContextAllIsolated && value
			}
			if observation["task_id_after_restart"] == childID {
				checks["same_task_after_restart"] = true
			}
			if value, ok := observation["skill_metadata_only"].(bool); ok {
				skillMetadataObserved = true
				skillMetadataOnly = skillMetadataOnly && value
			}
			if observation["skill_body_loaded"] == true {
				skillBodyLoaded = true
			}
			if observation["memory_recalled_after_restart"] == true {
				memoryRecalled = true
			}
			if value, ok := observation["reflection_without_tools"].(bool); ok {
				reflectionObserved = true
				reflectionWithoutTools = reflectionWithoutTools && value
			}
		}
	}
	checks["child_context_isolated"] = childContextObserved && childContextAllIsolated
	checks["skill_lazy_loading"] = skillMetadataObserved && skillMetadataOnly
	checks["reflection_without_tools"] = reflectionObserved && reflectionWithoutTools
	ledger, err = session.OpenSQLiteEventLog(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ = ledger.SessionIDs(context.Background())
	checks["ledger_hash_chains"] = true
	var childEvents []session.Event
	for _, id := range ids {
		if err := ledger.Verify(context.Background(), id); err != nil {
			checks["ledger_hash_chains"] = false
		}
		if id == childID {
			childEvents, _ = ledger.Events(context.Background(), id)
		}
		if !strings.HasPrefix(id, "memory-user-") {
			continue
		}
		events, _ := ledger.Events(context.Background(), id)
		for _, event := range events {
			if event.Type != "memory/catalog-updated" {
				continue
			}
			var mutation struct {
				Entries []memory.Experience `json:"entries"`
			}
			if json.Unmarshal(event.Payload, &mutation) == nil {
				for _, item := range mutation.Entries {
					if item.Kind == "patterns" && item.Key == "agents/independent-history" && item.Revision >= 2 {
						checks["typed_memory_recovered"] = true
					}
				}
			}
		}
	}
	_ = ledger.Close()
	releaseChecks["agent_card"], releaseChecks["message_text_file_json"] = independentAgentTaskContract(childEvents)
	releaseChecks["task_lifecycle"], releaseChecks["harness_authorized_tool"], releaseChecks["mcp_call"], releaseChecks["sandbox_enforced"] = independentAgentLifecycleAndToolAudit(childEvents)
	releaseChecks["isolated_child_context"] = checks["child_context_isolated"]
	releaseChecks["skill_lazy_loaded"] = skillMetadataObserved && skillMetadataOnly && skillBodyLoaded
	releaseChecks["artifact_pinned"] = checks["file_artifact_pinned"]
	releaseChecks["memory_reflection_written"] = checks["reflection_without_tools"] && checks["typed_memory_recovered"]
	releaseChecks["restart_recall"] = checks["same_task_after_restart"] && memoryRecalled
	releaseChecks["ledger_hash_chain"] = checks["ledger_hash_chains"]

	if !providerMode {
		for name, ok := range checks {
			if !ok {
				t.Errorf("process check failed: %s", name)
			}
		}
	}
}

func TestIndependentAgentProcessEnvIsAllowlisted(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "provider-value")
	t.Setenv("ANTHROPIC_API_KEY", "other-provider-value")
	t.Setenv("GITHUB_TOKEN", "unrelated-value")
	t.Setenv("CODE_AGENT_PROVIDER_CONFIG", "provider.json")
	t.Setenv("CODE_AGENT_RUN_INDEPENDENT_AGENTS_E2E", "1")
	t.Setenv("CODE_AGENT_E2E_SANDBOX_BACKEND", "docker")
	t.Setenv("CODE_AGENT_E2E_SANDBOX_IMAGE", "alpine:3.20")
	t.Setenv("CODE_AGENT_E2E_WSL_DISTRO", "must-not-pass-for-docker")
	t.Setenv("CODE_AGENT_E2E_SANDBOX_SECRET", "must-not-pass")
	t.Setenv("CODE_AGENT_EVAL_TRACEPARENT", "00-11111111111111111111111111111111-2222222222222222-01")

	names := func(values []string) map[string]bool {
		result := make(map[string]bool)
		for _, value := range values {
			name, _, _ := strings.Cut(value, "=")
			result[strings.ToUpper(name)] = true
		}
		return result
	}
	fixture := names(independentAgentProcessEnv("", ""))
	if !fixture["PATH"] || !fixture["CODE_AGENT_RUN_INDEPENDENT_AGENTS_E2E"] {
		t.Fatal("fixture environment dropped required process keys")
	}
	for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GITHUB_TOKEN", "CODE_AGENT_PROVIDER_CONFIG", "CODE_AGENT_E2E_SANDBOX_BACKEND", "CODE_AGENT_E2E_SANDBOX_IMAGE", "CODE_AGENT_E2E_WSL_DISTRO", "CODE_AGENT_E2E_SANDBOX_SECRET", "CODE_AGENT_EVAL_TRACEPARENT"} {
		if fixture[name] {
			t.Fatalf("fixture environment leaked %s", name)
		}
	}

	provider := names(independentAgentProcessEnv("", "openai"))
	for _, name := range []string{"OPENAI_API_KEY", "CODE_AGENT_E2E_SANDBOX_BACKEND", "CODE_AGENT_E2E_SANDBOX_IMAGE", "CODE_AGENT_EVAL_TRACEPARENT"} {
		if !provider[name] {
			t.Fatalf("provider environment dropped %s", name)
		}
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "CODE_AGENT_PROVIDER_CONFIG", "CODE_AGENT_E2E_WSL_DISTRO", "CODE_AGENT_E2E_SANDBOX_SECRET"} {
		if provider[name] {
			t.Fatalf("provider environment leaked %s", name)
		}
	}
}

func TestIndependentAgentProviderRouteRequiresSameRunAndBothSessions(t *testing.T) {
	event := func(sessionID, runID string) string {
		body, err := json.Marshal(map[string]any{
			"schema_version": 1, "phase": "model_after", "eval_run_id": runID,
			"session_id": sessionID, "turn": 1, "provider_backed": true,
			"provider": "openai", "model": "model-1",
			"model_identity": map[string]any{"requested_model": "model-1", "reported_model": "model-1-revision", "response_id": "response-1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	valid := event("parent", "run-1") + "\n" + event("child", "run-1") + "\n"
	if count, ok := independentAgentProviderRoute([]byte(valid), "run-1", "openai", "parent", "child"); count != 2 || !ok {
		t.Fatalf("valid provider route rejected: count=%d ok=%v", count, ok)
	}
	spliced := event("parent", "other-run") + "\n" + event("child", "run-1") + "\n"
	if _, ok := independentAgentProviderRoute([]byte(spliced), "run-1", "openai", "parent", "child"); ok {
		t.Fatal("cross-run provider evidence was accepted")
	}
}

func TestIndependentAgentMCPFixtureUsesProductionManager(t *testing.T) {
	repo := e2ERepositoryRoot(t)
	configPath := filepath.Join(t.TempDir(), ".mcp.json")
	body, err := json.Marshal(map[string]any{"servers": map[string]any{"e2e": map[string]any{
		"command":     contextE2EPython(t),
		"args":        []string{filepath.Join(repo, "tests", "e2e", "independent_agents_runtime_server.py"), "--mcp-stdio"},
		"working_dir": repo,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	manager := mcp.NewManager()
	if err := manager.LoadConfigFile(configPath); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := manager.Start(ctx, "e2e"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop("e2e") })
	result, err := manager.CallTool(ctx, "e2e_echo", map[string]any{"text": independentAgentMCPEchoMarker})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, independentAgentMCPEchoMarker) {
		t.Fatalf("unexpected MCP result: %#v", result)
	}
}

func independentAgentProcessEnv(_ string, provider string) []string {
	base := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true,
		"TEMP": true, "TMP": true, "USERPROFILE": true, "HOME": true, "LOCALAPPDATA": true, "APPDATA": true,
		"GOROOT": true, "GOPATH": true, "GOCACHE": true, "GOMODCACHE": true, "VIRTUAL_ENV": true,
		"PYTHONHOME": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "REQUESTS_CA_BUNDLE": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "LANG": true, "LC_ALL": true,
	}
	providerKeys := map[string]bool{}
	if provider == "openai" {
		for _, name := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_MODEL", "OPENAI_TIMEOUT", "OPENAI_MAX_RETRIES", "OPENAI_MAX_TOKENS", "OPENAI_REASONING_EFFORT"} {
			providerKeys[name] = true
		}
	} else if provider == "anthropic" {
		for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "ANTHROPIC_TIMEOUT", "ANTHROPIC_MAX_RETRIES", "ANTHROPIC_MAX_TOKENS", "ANTHROPIC_VERSION"} {
			providerKeys[name] = true
		}
	}
	joinKeys := map[string]bool{
		"CODE_AGENT_RUN_INDEPENDENT_AGENTS_E2E":                    true,
		"CODE_AGENT_E2E_PROVIDER":                                  true,
		"CODE_AGENT_EVAL_RUN_ID":                                   true,
		"CODE_AGENT_INDEPENDENT_AGENTS_E2E_RUN_ID":                 true,
		"CODE_AGENT_INDEPENDENT_AGENTS_E2E_RECEIPT":                true,
		"CODE_AGENT_INDEPENDENT_AGENTS_E2E_PROVIDER_ROUTE_RECEIPT": true,
		"PHOENIX_URL": true, "PHOENIX_PROJECT": true,
		"OTEL_EXPORTER_OTLP_ENDPOINT": true, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": true,
		"OTEL_EXPORTER_OTLP_PROTOCOL": true, "OTEL_SERVICE_NAME": true,
	}
	if provider == "openai" || provider == "anthropic" {
		joinKeys["CODE_AGENT_E2E_SANDBOX_BACKEND"] = true
		joinKeys["CODE_AGENT_E2E_SANDBOX_IMAGE"] = true
		joinKeys["CODE_AGENT_EVAL_TRACEPARENT"] = true
		if strings.EqualFold(strings.TrimSpace(os.Getenv("CODE_AGENT_E2E_SANDBOX_BACKEND")), "wsl2") {
			joinKeys["CODE_AGENT_E2E_WSL_DISTRO"] = true
		}
	}
	result := []string{}
	for _, value := range os.Environ() {
		name, _, found := strings.Cut(value, "=")
		if !found {
			continue
		}
		upper := strings.ToUpper(name)
		if base[upper] || providerKeys[upper] || joinKeys[upper] {
			result = append(result, value)
		}
	}
	return result
}

func independentAgentProviderRoute(body []byte, runID, provider, parentID, childID string) (int, bool) {
	if provider == "" || parentID == "" || childID == "" || parentID == childID {
		return 0, false
	}
	count := 0
	valid := true
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			SchemaVersion  int            `json:"schema_version"`
			Phase          string         `json:"phase"`
			EvalRunID      string         `json:"eval_run_id"`
			SessionID      string         `json:"session_id"`
			Turn           int            `json:"turn"`
			ProviderBacked bool           `json:"provider_backed"`
			Provider       string         `json:"provider"`
			Model          string         `json:"model"`
			Identity       map[string]any `json:"model_identity"`
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Phase != "model_after" {
			valid = false
			continue
		}
		count++
		requested, _ := event.Identity["requested_model"].(string)
		reported, _ := event.Identity["reported_model"].(string)
		responseID, _ := event.Identity["response_id"].(string)
		if event.SchemaVersion != 1 || event.EvalRunID != runID || !event.ProviderBacked || event.Provider != provider || event.Turn < 1 || event.Model == "" || requested != event.Model || reported == "" || responseID == "" {
			valid = false
		}
		if event.SessionID != parentID && event.SessionID != childID {
			valid = false
		} else {
			seen[event.SessionID] = true
		}
	}
	return count, valid && count > 0 && seen[parentID] && seen[childID]
}

func independentAgentTaskContract(events []session.Event) (bool, bool) {
	for _, event := range events {
		if event.Type != "agent/task-created" {
			continue
		}
		var envelope struct {
			Task json.RawMessage `json:"task"`
		}
		if json.Unmarshal(event.Payload, &envelope) != nil {
			return false, false
		}
		var task struct {
			SchemaVersion string `json:"schema_version"`
			Card          struct {
				ID             string   `json:"id"`
				Description    string   `json:"description"`
				Address        string   `json:"address"`
				Transports     []string `json:"transports"`
				Authentication []string `json:"authentication"`
			} `json:"card"`
			Messages []struct {
				Parts []struct {
					Text     string `json:"text"`
					DataJSON string `json:"data_json"`
					File     *struct {
						Path   string `json:"path"`
						SHA256 string `json:"sha256"`
					} `json:"file"`
				} `json:"parts"`
			} `json:"messages"`
		}
		if json.Unmarshal(envelope.Task, &task) != nil {
			return false, false
		}
		cardOK := task.SchemaVersion == "agent.v2" && task.Card.ID != "" && task.Card.Description != "" && strings.HasPrefix(task.Card.Address, "harness://agents/") && len(task.Card.Transports) > 0 && len(task.Card.Authentication) > 0
		textPart, filePart, dataPart := false, false, false
		for _, message := range task.Messages {
			for _, part := range message.Parts {
				textPart = textPart || strings.TrimSpace(part.Text) != ""
				dataPart = dataPart || json.Valid([]byte(part.DataJSON))
				filePart = filePart || part.File != nil && strings.HasPrefix(filepath.ToSlash(part.File.Path), ".agent/materials/") && len(part.File.SHA256) == 64
			}
		}
		return cardOK, textPart && filePart && dataPart
	}
	return false, false
}

func independentAgentLifecycleAndToolAudit(events []session.Event) (bool, bool, bool, bool) {
	types := map[string]bool{}
	type toolEvent struct {
		RunID      string `json:"run_id"`
		ToolCallID string `json:"tool_call_id"`
		ToolName   string `json:"tool_name"`
		Output     string `json:"output"`
		Error      string `json:"error"`
		ExitCode   int    `json:"exit_code"`
	}
	calls, dispatched, results := map[string]string{}, map[string]string{}, map[string]toolEvent{}
	for _, event := range events {
		types[event.Type] = true
		if event.Type != "tool/call" && event.Type != "tool/dispatched" && event.Type != "tool/result" {
			continue
		}
		var item toolEvent
		if json.Unmarshal(event.Payload, &item) != nil || item.ToolCallID == "" || item.ToolName == "" {
			continue
		}
		key := item.RunID + "\x00" + item.ToolCallID
		switch event.Type {
		case "tool/call":
			calls[key] = item.ToolName
		case "tool/dispatched":
			dispatched[key] = item.ToolName
		case "tool/result":
			results[key] = item
		}
	}
	lifecycle := types["agent/task-created"] && types["session/continued"] && types["session/run-leased"] && types["session/run-completed"] && types["agent/task-artifact"]
	authorized, mcpCall, sandboxEnforced := false, false, false
	for key, name := range calls {
		result, hasResult := results[key]
		complete := dispatched[key] == name && hasResult && result.ToolName == name && result.Error == "" && result.ExitCode == 0
		if !complete {
			continue
		}
		if name == "Skill" || name == "RecallMemory" || name == "Read" {
			authorized = true
		}
		mcpCall = mcpCall || name == "e2e_echo" && strings.Contains(result.Output, independentAgentMCPEchoMarker)
		sandboxEnforced = sandboxEnforced || name == "Bash" &&
			strings.Contains(result.Output, independentAgentSandboxMarker) &&
			strings.Contains(result.Output, independentAgentSandboxWriteBlockedMarker) &&
			!strings.Contains(result.Output, independentAgentSandboxWriteFailedMarker)
	}
	return lifecycle, authorized, mcpCall, sandboxEnforced
}

func TestIndependentAgentToolEvidenceRequiresCompleteChildLedgerChain(t *testing.T) {
	event := func(kind, name, output string) session.Event {
		payload, err := json.Marshal(map[string]any{
			"run_id": "child-run", "tool_call_id": "call-1", "tool_name": name,
			"output": output, "exit_code": 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		return session.Event{Type: kind, Payload: payload}
	}
	mcpEvents := []session.Event{
		event("tool/call", "e2e_echo", ""),
		event("tool/dispatched", "e2e_echo", ""),
		event("tool/result", "e2e_echo", "e2e_echo: "+independentAgentMCPEchoMarker),
	}
	_, _, mcpCall, sandboxEnforced := independentAgentLifecycleAndToolAudit(mcpEvents)
	if !mcpCall || sandboxEnforced {
		t.Fatalf("unexpected MCP/sandbox evidence: mcp=%v sandbox=%v", mcpCall, sandboxEnforced)
	}
	mcpEvents[1] = event("tool/dispatched", "Read", "")
	_, _, mcpCall, _ = independentAgentLifecycleAndToolAudit(mcpEvents)
	if mcpCall {
		t.Fatal("mismatched dispatch was accepted as MCP evidence")
	}
	bashEvents := []session.Event{
		event("tool/call", "Bash", ""),
		event("tool/dispatched", "Bash", ""),
		event("tool/result", "Bash", independentAgentSandboxMarker+"\n"+independentAgentSandboxWriteBlockedMarker+"\n"),
	}
	_, _, _, sandboxEnforced = independentAgentLifecycleAndToolAudit(bashEvents)
	if !sandboxEnforced {
		t.Fatal("complete sandboxed Bash chain was not recognized")
	}
	bashEvents[2] = event("tool/result", "Bash", independentAgentSandboxMarker+"\n")
	_, _, _, sandboxEnforced = independentAgentLifecycleAndToolAudit(bashEvents)
	if sandboxEnforced {
		t.Fatal("read-only sentinel without write refusal was accepted as sandbox evidence")
	}
}

func startIndependentAgentEvalTrace(t *testing.T, runID string) {
	t.Helper()
	telemetry := genai.NewTelemetry(context.Background())
	ctx := genai.WithEvalJoinBaggage(context.Background(), runID, "")
	ctx, span := telemetry.StartSpan(ctx, "eval.run", "eval.run", genai.SystemGenAI)
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	traceparent := carrier.Get("traceparent")
	if traceparent == "" {
		t.Fatal("provider lane requires an exportable eval.run trace")
	}
	t.Setenv("CODE_AGENT_EVAL_TRACEPARENT", traceparent)
	t.Cleanup(func() {
		span.End()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(shutdownCtx); err != nil {
			t.Errorf("flush eval.run trace: %v", err)
		}
	})
}
