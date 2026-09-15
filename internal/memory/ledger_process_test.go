package memory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/session"
)

type memoryProcessObservation struct {
	PID            int             `json:"pid"`
	SessionID      string          `json:"session_id"`
	EventCount     int             `json:"event_count"`
	FinalChecksum  string          `json:"final_event_checksum"`
	SourceChecksum string          `json:"source_checksum"`
	Checks         map[string]bool `json:"checks"`
}

func TestLedgerMemoryAcrossProcessRestart(t *testing.T) {
	if mode := os.Getenv("CODE_AGENT_MEMORY_TEST_PHASE"); mode != "" {
		runMemoryProcessPhase(t, mode, os.Getenv("CODE_AGENT_MEMORY_TEST_DIR"))
		return
	}
	if os.Getenv("CODE_AGENT_RUN_MEMORY_INTEGRATION") != "1" {
		t.Skip("set CODE_AGENT_RUN_MEMORY_INTEGRATION=1 for fixture-backed memory process integration")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rootBytes, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal("memory integration requires a Git source pin")
	}
	root := strings.TrimSpace(string(rootBytes))
	shaCommand := exec.Command("git", "rev-parse", "HEAD")
	shaCommand.Dir = root
	shaBytes, err := shaCommand.Output()
	if err != nil {
		t.Fatal("read memory integration source pin")
	}
	outputRoot := filepath.Join(root, ".runtime", "e2e")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(outputRoot, "memory-ledger-process-")
	if err != nil {
		t.Fatal(err)
	}
	runID := filepath.Base(directory)
	observations := make(map[string]memoryProcessObservation)
	processes := make([]map[string]any, 0, 2)
	processesSucceeded := true
	for _, phase := range []string{"seed", "recover"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		command := exec.CommandContext(ctx, executable, "-test.run=^TestLedgerMemoryAcrossProcessRestart$", "-test.count=1")
		command.Dir = root
		command.Env = append(os.Environ(), "CODE_AGENT_MEMORY_TEST_PHASE="+phase, "CODE_AGENT_MEMORY_TEST_DIR="+directory)
		output, runErr := command.CombinedOutput()
		cancel()
		if err := os.WriteFile(filepath.Join(directory, phase+".log"), output, 0o600); err != nil {
			t.Fatal(err)
		}
		exitCode, pid := -1, 0
		if command.ProcessState != nil {
			exitCode, pid = command.ProcessState.ExitCode(), command.ProcessState.Pid()
		}
		processes = append(processes, map[string]any{"phase": phase, "pid": pid, "exit_code": exitCode})
		processesSucceeded = processesSucceeded && runErr == nil
		data, readErr := os.ReadFile(filepath.Join(directory, phase+".json"))
		var observation memoryProcessObservation
		if readErr == nil && json.Unmarshal(data, &observation) == nil {
			observations[phase] = observation
		}
	}
	seed, recovered := observations["seed"], observations["recover"]
	checks := map[string]bool{
		"independent_processes": seed.PID != 0 && recovered.PID != 0 && seed.PID != recovered.PID,
		"process_exits":         processesSucceeded,
	}
	for name, passed := range seed.Checks {
		checks[name] = passed
	}
	for name, passed := range recovered.Checks {
		checks[name] = passed
	}
	checks["source_provenance"] = checks["source_provenance"] && seed.SessionID != "" && seed.SessionID == recovered.SessionID && seed.SourceChecksum != "" && seed.SourceChecksum == recovered.SourceChecksum
	checkNames := []string{
		"independent_processes", "process_exits", "missed_commit_after_terminal",
		"recovered_commit", "owner_isolation", "token_budget", "hash_chain",
		"source_provenance", "idempotent_recovery", "recovery_without_model",
	}
	results := make([]map[string]string, 0, len(checkNames))
	passedCount := 0
	for _, name := range checkNames {
		status := "not_run"
		if passed, observed := checks[name]; observed {
			status = "failed"
			if passed {
				status = "passed"
				passedCount++
			}
		}
		results = append(results, map[string]string{"name": name, "status": status})
	}
	artifacts := make([]map[string]string, 0, 5)
	for _, name := range []string{"ledger.sqlite", "seed.json", "recover.json", "seed.log", "recover.log"} {
		data, readErr := os.ReadFile(filepath.Join(directory, name))
		if readErr != nil {
			continue
		}
		artifacts = append(artifacts, map[string]string{
			"path":   filepath.ToSlash(filepath.Join(".runtime", "e2e", runID, name)),
			"sha256": fmt.Sprintf("%x", sha256.Sum256(data)),
		})
	}
	sourceHashes := make(map[string]string)
	for _, name := range []string{
		"internal/memory/ledger.go", "internal/memory/trajectory.go", "internal/memory/ledger_test.go",
		"internal/memory/ledger_process_test.go", "internal/session/eventlog.go",
		"internal/session/ledger_snapshot.go", "internal/session/memory.go", "internal/session/runner.go",
	} {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if readErr != nil {
			t.Fatal("read memory integration source checksum")
		}
		sourceHashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	dirtyCommand := exec.Command("git", "status", "--porcelain", "--", "internal/memory", "internal/session")
	dirtyCommand.Dir = root
	dirtyBytes, dirtyErr := dirtyCommand.Output()
	status, exitCode := "IMPLEMENTED", 0
	if passedCount != len(checkNames) {
		status, exitCode = "BLOCKED", 1
	}
	receipt := map[string]any{
		"schema_version": 1, "status": status, "exit_code": exitCode,
		"kind":           "fixture-backed-memory-ledger-process-integration",
		"scope":          "Go SessionRunner and LedgerMemory across independent test processes; not a production entry point or provider-backed E2E",
		"fixture_backed": true, "provider_backed": false,
		"git_sha": strings.TrimSpace(string(shaBytes)), "source_dirty": dirtyErr != nil || len(dirtyBytes) > 0,
		"source_sha256": sourceHashes, "run_id": runID,
		"command":      "CODE_AGENT_RUN_MEMORY_INTEGRATION=1 go test ./internal/memory -run TestLedgerMemoryAcrossProcessRestart -count=1",
		"checks_total": len(checkNames), "checks_passed": passedCount, "checks": results,
		"processes": processes, "session_id": recovered.SessionID,
		"event_count": recovered.EventCount, "final_event_checksum": recovered.FinalChecksum,
		"source_checksum": recovered.SourceChecksum, "budget_tokens": 1200, "artifacts": artifacts,
	}
	writeMemoryProcessJSON(t, filepath.Join(directory, "receipt.json"), receipt)
	writeMemoryProcessJSON(t, filepath.Join(outputRoot, "memory-ledger-process.json"), receipt)
	for _, result := range results {
		if result["status"] != "passed" {
			t.Errorf("memory integration check %s: %s; evidence retained in ignored runtime directory", result["name"], result["status"])
		}
	}
}

func runMemoryProcessPhase(t *testing.T, phase, directory string) {
	t.Helper()
	if directory == "" || (phase != "seed" && phase != "recover") {
		t.Fatal("invalid memory integration phase")
	}
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(directory, "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	module := NewLedgerMemory(ledger)
	observation := memoryProcessObservation{PID: os.Getpid(), Checks: make(map[string]bool)}
	if phase == "seed" {
		observation.SessionID = runMemoryFixture(t, ledger, 7, "fixture durable anchor", nil, &memoryConversationFixture{})
		runMemoryFixture(t, ledger, 8, "fixture foreign private anchor", module, &memoryConversationFixture{})
		trajectory, buildErr := BuildSessionTrajectory(ctx, ledger, observation.SessionID, TrajectoryOptions{})
		observation.SourceChecksum = trajectory.SourceChecksum
		missing := decodeMemoryRecall(t, module, 7, "anchor", 1200)
		observation.Checks["missed_commit_after_terminal"] = buildErr == nil && trajectory.Complete && len(missing.Entries) == 0
	} else {
		views, listErr := session.NewWorkbench(ledger, nil).List(ctx, 7)
		if listErr != nil || len(views) != 1 {
			t.Fatal("recover owned fixture session")
		}
		observation.SessionID = views[0].ID
		// No conversation adapter exists in this process. Recovery must only
		// supplement the missing immutable commit, never execute the model.
		runner := session.NewSessionRunner(session.NewWorkbench(ledger, nil), nil, nil, session.SessionRunnerOptions{Memory: module})
		defer runner.Close()
		if err := runner.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		result := decodeMemoryRecall(t, module, 7, "anchor", 1200)
		observation.Checks["owner_isolation"] = len(result.Entries) == 1 && result.Entries[0].Memory.SessionID == observation.SessionID && !strings.Contains(result.Entries[0].Memory.Content, "foreign private")
		if len(result.Entries) == 1 {
			observation.SourceChecksum = result.Entries[0].Memory.SourceChecksum
		}
		tiny := decodeMemoryRecall(t, module, 7, "anchor", 1)
		observation.Checks["token_budget"] = tiny.Stats.Returned == 0 && tiny.Stats.Dropped == 1 && tiny.Stats.UsedTokens <= 1
		before, readErr := session.ReadVerifiedSnapshot(ctx, ledger, observation.SessionID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		observation.Checks["hash_chain"] = true
		commits := 0
		for _, event := range before.Events {
			if event.Type == trajectoryCommittedEvent {
				commits++
			}
		}
		observation.Checks["recovered_commit"] = commits == 1
		observation.Checks["recovery_without_model"] = commits == 1 && len(result.Entries) == 1
		trajectory, buildErr := BuildSessionTrajectory(ctx, ledger, observation.SessionID, TrajectoryOptions{})
		observation.Checks["source_provenance"] = buildErr == nil && trajectory.Complete && trajectory.SourceChecksum != "" && trajectory.SourceChecksum == observation.SourceChecksum
		if err := runner.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		after, readErr := session.ReadVerifiedSnapshot(ctx, ledger, observation.SessionID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		observation.Checks["idempotent_recovery"] = len(before.Events) == len(after.Events)
		observation.EventCount = len(after.Events)
		observation.FinalChecksum = after.Events[len(after.Events)-1].Checksum
	}
	snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, observation.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	observation.EventCount = len(snapshot.Events)
	observation.FinalChecksum = snapshot.Events[len(snapshot.Events)-1].Checksum
	writeMemoryProcessJSON(t, filepath.Join(directory, phase+".json"), observation)
}

func writeMemoryProcessJSON(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
