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

	"code-agent/internal/memory"
	"code-agent/internal/session"
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
	checks := map[string]bool{
		"production_go_cli": false, "independent_child_session": false, "child_context_isolated": false,
		"input_required_followup": false, "skill_lazy_loading": false, "file_artifact_pinned": false,
		"two_go_processes": false, "two_python_processes": false, "same_task_after_restart": false,
		"child_history_after_restart": false, "reflection_without_tools": false,
		"typed_memory_recovered": false, "ledger_hash_chains": false,
	}
	receipt := map[string]any{"status": "IMPLEMENTED", "evidence_type": "fixture-backed-production-cli-grpc",
		"provider_backed": false, "git_sha": contextE2EGitSHA(t, repo), "run_id": filepath.Base(runRoot),
		"command": "CODE_AGENT_RUN_INDEPENDENT_AGENTS_E2E=1 go test ./tests/e2e -run TestProductionIndependentAgentsAndMemoryProcess -count=1",
		"checks":  checks, "artifacts": runRoot}
	defer func() {
		passed := 0
		for _, ok := range checks {
			if ok {
				passed++
			}
		}
		receipt["passed"] = passed
		receipt["total"] = len(checks)
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
		body, _ := json.MarshalIndent(receipt, "", "  ")
		for _, path := range []string{filepath.Join(runRoot, "receipt.json"), filepath.Join(repo, ".runtime", "e2e", "independent-agents-process.json")} {
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
	if err := os.MkdirAll(filepath.Join(project, ".agent"), 0700); err != nil {
		t.Fatal(err)
	}
	address := contextE2EAddress(t)
	_, port, _ := net.SplitHostPort(address)
	dbPath := filepath.Join(project, ".agent", "sessions.sqlite")
	settings, _ := json.Marshal(map[string]any{"session_db_path": dbPath, "memory_dir": filepath.Join(project, ".agent", "legacy-memory"),
		"orchestrator_addr": address, "orchestrator_auto_start": false, "orchestrator_conversation_timeout_seconds": 30,
		"mcp_config": "missing-mcp.json", "sandbox": map[string]any{"enabled": false},
		"permissions": map[string]any{"allow": []map[string]string{{"tool": "SpawnAgent"}, {"tool": "Skill"}}}})
	write(filepath.Join(project, ".agent", "settings.local.json"), settings)
	env := []string{}
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "CODE_AGENT_") || strings.HasPrefix(upper, "OPENAI_") || strings.HasPrefix(upper, "ANTHROPIC_") || strings.HasPrefix(upper, "OTEL_") || strings.HasPrefix(upper, "MODEL_") || strings.Contains(upper, "API_KEY") {
			continue
		}
		env = append(env, value)
	}
	env = append(env, "PYTHONPATH="+repo)
	pythonPIDs, goPIDs, goExitCodes := []int{}, []int{}, []int{}
	receipt["python_pids"], receipt["go_pids"], receipt["go_exit_codes"] = pythonPIDs, goPIDs, goExitCodes
	startServer := func(stage int) func() {
		t.Helper()
		logPath := filepath.Join(runRoot, fmt.Sprintf("python-%d.log", stage))
		log, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(contextE2EPython(t), filepath.Join(repo, "tests", "e2e", "independent_agents_runtime_server.py"), "--port", port, "--project-root", project,
			"--observations", filepath.Join(runRoot, fmt.Sprintf("observations-%d.jsonl", stage)))
		cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = project, env, log, log
		if err := cmd.Start(); err != nil {
			_ = log.Close()
			t.Fatal(err)
		}
		pythonPIDs = append(pythonPIDs, cmd.Process.Pid)
		receipt["python_pids"] = pythonPIDs
		stopped := false
		stop := func() {
			if !stopped {
				stopped = true
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				_ = log.Close()
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
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	runCLI("DELEGATE_AGENT_CASE PARENT_PRIVATE_HISTORY\n", 1)
	checks["production_go_cli"] = true
	ledger, err := session.OpenSQLiteEventLog(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ := ledger.SessionIDs(context.Background())
	parentID, childID := "", ""
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
	runCLI("/resume "+parentID+"\nRESUME_AGENT_CASE\n", 2)
	stopSecond()
	checks["two_go_processes"] = len(goPIDs) == 2 && goPIDs[0] != goPIDs[1]
	checks["two_python_processes"] = len(pythonPIDs) == 2 && pythonPIDs[0] != pythonPIDs[1]
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
			for source, target := range map[string]string{"child_context_isolated": "child_context_isolated", "task_input_required": "input_required_followup", "skill_metadata_only": "skill_lazy_loading",
				"file_artifact_pinned": "file_artifact_pinned", "restart_history": "child_history_after_restart", "reflection_without_tools": "reflection_without_tools"} {
				if observation[source] == true {
					checks[target] = true
				}
			}
			if observation["task_id_after_restart"] == childID {
				checks["same_task_after_restart"] = true
			}
		}
	}
	ledger, err = session.OpenSQLiteEventLog(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ids, _ = ledger.SessionIDs(context.Background())
	checks["ledger_hash_chains"] = true
	for _, id := range ids {
		if err := ledger.Verify(context.Background(), id); err != nil {
			checks["ledger_hash_chains"] = false
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
	for name, ok := range checks {
		if !ok {
			t.Errorf("process check failed: %s", name)
		}
	}
}
