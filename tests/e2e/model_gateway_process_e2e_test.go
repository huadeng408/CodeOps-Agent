package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/session"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestProductionModelGatewayAcrossProcesses(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_MODEL_GATEWAY_E2E") != "1" {
		t.Skip("explicit opt-in required: this test makes paid model calls")
	}
	repo := e2ERepositoryRoot(t)
	base := filepath.Join(repo, ".runtime", "e2e", "model-gateway-runs")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "run-")
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{"foreground": false, "memory_reflection_call": false, "compaction_call": false,
		"workflow_worker_call": false, "fresh_python_process": false, "go_process_recovery": false, "ledger_hash_chain": false, "unknown_price": false}
	receipt := map[string]any{"git_sha": contextE2EGitSHA(t, repo), "run_id": filepath.Base(root),
		"evidence_type": "real-provider-go-gateway-python-client", "checks": checks,
		"command":      "CODE_AGENT_RUN_MODEL_GATEWAY_E2E=1 go test ./tests/e2e -run TestProductionModelGatewayAcrossProcesses -count=1 -v",
		"trace_status": "unknown", "product_browser_verified": false}
	hashes := modelGatewaySourceHashes(t, repo)
	receipt["source_sha256"] = hashes
	defer func() {
		// This probe does not read a complete trace from a real backend.
		receipt["exit_code"], receipt["status"] = 0, "BLOCKED"
		if t.Failed() {
			receipt["exit_code"], receipt["status"] = 1, "BLOCKED"
		}
		passed := 0
		for _, ok := range checks {
			if ok {
				passed++
			}
		}
		receipt["passed"], receipt["total"] = passed, len(checks)
		unchanged := reflect.DeepEqual(hashes, modelGatewaySourceHashes(t, repo))
		receipt["source_unchanged"] = unchanged
		if !unchanged {
			t.Error("source changed during the runtime probe")
			receipt["exit_code"] = 1
		}
		data, _ := json.MarshalIndent(receipt, "", "  ")
		if err := os.WriteFile(filepath.Join(root, "receipt.json"), data, 0600); err != nil {
			t.Error(err)
		}
		t.Logf("receipt: %s", filepath.Join(root, "receipt.json"))
	}()
	config := admission.ProviderFromEnv()
	if config.InputLimit <= 0 || config.OutputLimit <= 0 || config.Model == "" || config.APIKey == "" {
		t.Fatal("approved provider and explicit token bounds are required")
	}
	receipt["provider"], receipt["model"] = config.Protocol, config.Model
	receipt["input_bound"], receipt["output_bound"] = config.InputLimit, config.OutputLimit
	dbPath := os.Getenv("CODE_AGENT_MODEL_GATEWAY_LEDGER")
	if dbPath == "" {
		t.Fatal("one canonical paid-verification Ledger must be configured across runs")
	}
	ledger, err := session.OpenSQLiteEventLog(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	view, err := session.NewWorkbench(ledger, nil).CreateWithWorkingDir(context.Background(), 7, "probe", "Model admission", "Bounded calls", root)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	before, err := budget.Open(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	receipt["batch_before"] = before
	gateway := admission.NewGateway(budget, config)
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	var pythonPIDs []int
	for stage, purposes := range [][]string{{"foreground", "memory_reflection", "compaction", "workflow_worker"}, {"foreground"}} {
		binding, release, err := gateway.BindActor(actor, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := protojson.Marshal(binding)
		input, _ := json.Marshal(map[string]any{"binding": json.RawMessage(encoded), "purposes": purposes})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		command := exec.CommandContext(ctx, "python", "-m", "tests.e2e.model_gateway_probe")
		command.Dir, command.Stdin = repo, bytes.NewReader(input)
		for _, item := range os.Environ() {
			name, _, _ := strings.Cut(item, "=")
			if strings.Contains(strings.ToUpper(name), "API_KEY") || name == "ANTHROPIC_AUTH_TOKEN" || name == "CODE_AGENT_PROVIDER_CONFIG" {
				continue
			}
			command.Env = append(command.Env, item)
		}
		command.Env = append(command.Env, "CODE_AGENT_MODEL_ADMISSION=required", "OTEL_SDK_DISABLED=true", "THINKING_ENABLED=false")
		output, runErr := command.Output()
		cancel()
		release()
		var observed struct {
			PID     int  `json:"pid"`
			Passed  bool `json:"passed"`
			Results []struct {
				Purpose   string `json:"purpose"`
				Passed    bool   `json:"passed"`
				ErrorCode string `json:"error_code"`
			} `json:"results"`
		}
		if json.Unmarshal(output, &observed) != nil {
			t.Fatal("Python probe returned no safe result; raw subprocess output suppressed")
		}
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("python-%d.json", stage+1)), output, 0600); err != nil {
			t.Fatal(err)
		}
		pythonPIDs = append(pythonPIDs, observed.PID)
		receipt["python_pids"] = pythonPIDs
		for _, result := range observed.Results {
			key := result.Purpose + "_call"
			if result.Purpose == "foreground" {
				key = "foreground"
			}
			checks[key] = result.Passed
			if !result.Passed {
				receipt["error_code"] = result.ErrorCode
			}
		}
		batch, batchErr := budget.Open(context.Background(), 7)
		receipt["batch"] = batch
		if batchErr != nil || runErr != nil || !observed.Passed {
			t.Fatal("real provider probe failed; reservation and failure denominator retained")
		}
	}
	checks["fresh_python_process"] = len(pythonPIDs) == 2 && pythonPIDs[0] != pythonPIDs[1]
	batch, err := budget.Open(context.Background(), 7)
	if err != nil || batch.UsedTokens <= 0 || batch.ReservedTokens != 0 || batch.UnknownUsage {
		t.Fatal("confirmed usage was not durably settled")
	}
	checks["unknown_price"] = batch.CostStatus == "unknown"
	checks["ledger_hash_chain"] = ledger.Verify(context.Background(), session.VerificationAdmissionLedgerID) == nil
	snapshot, err := session.ReadVerifiedSnapshot(context.Background(), ledger, session.VerificationAdmissionLedgerID)
	if err != nil {
		t.Fatal(err)
	}
	var attempts []json.RawMessage
	for _, event := range snapshot.Events {
		if event.Type == "admission/call_reserved" {
			var fact struct {
				TaskID string `json:"task_id"`
			}
			if json.Unmarshal(event.Payload, &fact) == nil && fact.TaskID == batch.TaskID {
				attempts = append(attempts, event.Payload)
			}
		}
	}
	receipt["attempts"], receipt["model_call_count"] = attempts, len(attempts)
	receipt["used_tokens_delta"] = batch.UsedTokens - before.UsedTokens
	if _, err := budget.FinishTask(context.Background(), batch.TaskOwnerID, batch.TaskID); err != nil {
		t.Fatal("cannot release the fully settled probe task")
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestModelGatewayRestoreHelper$")
	command.Env = append(os.Environ(), "CODE_AGENT_MODEL_GATEWAY_RESTORE_DB="+dbPath)
	output, err := command.Output()
	if err != nil {
		t.Fatal("Go recovery helper failed; raw output suppressed")
	}
	for _, line := range strings.Split(string(output), "\n") {
		if body, ok := strings.CutPrefix(line, "RECOVERED_BATCH:"); ok {
			var recovered admission.Batch
			if json.Unmarshal([]byte(body), &recovered) == nil {
				checks["go_process_recovery"] = recovered.ID == batch.ID && recovered.UsedTokens == batch.UsedTokens && recovered.ReservedTokens == 0
			}
		}
	}
	for _, passed := range checks {
		if !passed {
			t.Fatal("incomplete gateway runtime denominator")
		}
	}
}

func TestModelGatewaySourceManifestIncludesCommittedCode(t *testing.T) {
	hashes := modelGatewaySourceHashes(t, e2ERepositoryRoot(t))
	for _, path := range []string{"internal/admission/gateway.go", "orchestrator/llm/gateway.py", "proto/codeagent/orchestrator.proto"} {
		if len(hashes[path]) != sha256.Size*2 {
			t.Errorf("committed source missing from manifest: %s", path)
		}
	}
}

func modelGatewaySourceHashes(t *testing.T, repo string) map[string]string {
	t.Helper()
	hashes := map[string]string{}
	for _, args := range [][]string{{"ls-files"}, {"ls-files", "--others", "--exclude-standard"}} {
		command := exec.Command("git", args...)
		command.Dir = repo
		paths, err := command.Output()
		if err != nil {
			t.Fatal("source manifest unavailable")
		}
		for _, path := range strings.Split(strings.TrimSpace(string(paths)), "\n") {
			if path == "" || !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".py") && !strings.HasSuffix(path, ".proto") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(repo, path))
			if err != nil {
				t.Fatal("source hash unavailable")
			}
			hashes[path] = fmt.Sprintf("%x", sha256.Sum256(data))
		}
	}
	return hashes
}

func TestModelGatewayRestoreHelper(t *testing.T) {
	path := os.Getenv("CODE_AGENT_MODEL_GATEWAY_RESTORE_DB")
	if path == "" {
		t.Skip("subprocess helper; no model requests")
	}
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	batch, err := admission.NewBudget(ledger).Open(context.Background(), 7)
	if err != nil {
		t.Fatal("could not recover verified batch")
	}
	data, _ := json.Marshal(batch)
	fmt.Printf("RECOVERED_BATCH:%s\n", data)
}
