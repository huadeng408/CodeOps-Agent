package admission_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/admission"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func TestTaskCompletionRecoversOnlyConfirmedStoppedScopeProcess(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("OS process identity is not supported on this platform")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "fixture", "scope recovery", "")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := startModelScopeHolder(t, ctx, path, created.ID, false)
	budget := admission.NewBudget(ledger)
	before, err := budget.Inspect(ctx, 7)
	if err != nil || before.UsedTokens != 11 || before.ReservedTokens != 0 || before.TaskID != created.ID {
		t.Fatal("the helper did not settle its independent fixture call")
	}
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if err := gateway.FinishTask(ctx, actor); !errors.Is(err, admission.ErrTaskBusy) {
		t.Fatalf("live scope process was released: %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err = session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget = admission.NewBudget(ledger)
	gateway = admission.NewGateway(budget, admission.ProviderConfig{})
	if err := gateway.FinishTask(ctx, actor); err != nil {
		t.Fatalf("confirmed stopped Go holder kept its task blocked after reopen: %v", err)
	}
	after, err := budget.Inspect(ctx, 7)
	if err != nil || after.ID != before.ID || after.UsedTokens != 11 || after.ReservedTokens != 0 || after.TaskID != "" || after.CostStatus != "unknown" {
		t.Fatal("scope recovery reset or changed the settled fixture batch")
	}
}

func startModelScopeHolder(t *testing.T, ctx context.Context, path, sessionID string, unknown bool) *exec.Cmd {
	t.Helper()
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("OS process identity is not supported on this platform")
	}
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModelScopeProcessHelper$", "-test.v")
	command.Env = append(os.Environ(), "CODE_AGENT_SCOPE_TEST_DB="+path, "CODE_AGENT_SCOPE_TEST_SESSION="+sessionID)
	if unknown {
		command.Env = append(command.Env, "CODE_AGENT_SCOPE_TEST_USAGE_UNKNOWN=1")
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		_ = input.Close()
	})
	scanner := bufio.NewScanner(output)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "SCOPE_READY" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("test-owned scope process did not become ready")
	}
	return command
}

func TestStoppedScopeHolderCannotReconcileUnknownModelUsage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "fixture", "unknown usage", "")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := startModelScopeHolder(t, ctx, path, created.ID, true)
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	budget := admission.NewBudget(ledger)
	before, err := budget.Inspect(ctx, 7)
	if err != nil || before.UsedTokens != 0 || before.ReservedTokens != 103 || !before.UnknownUsage {
		t.Fatal("fixture unknown usage was not retained")
	}
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if err := gateway.FinishTask(ctx, actor); err != nil {
		t.Fatalf("terminal unknown usage prevented stopped-holder recovery: %v", err)
	}
	after, err := budget.Inspect(ctx, 7)
	if err != nil || after.ID != before.ID || after.UsedTokens != before.UsedTokens || after.ReservedTokens != before.ReservedTokens || !after.UnknownUsage || after.TaskID != "" || after.CostStatus != "unknown" {
		t.Fatal("stopped holder recovery changed unknown usage or its complete reservation")
	}
}

func TestStoppedScopeHolderRetainsCrashReservationAsUnknown(t *testing.T) {
	for _, scenario := range []string{"retained", "released"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "fixture", "crash before accounting", "")
			if err != nil {
				t.Fatal(err)
			}
			actor, err := identity.Default().BindSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("CODE_AGENT_SCOPE_TEST_CRASH_WINDOW", scenario)
			command := startModelScopeHolder(t, ctx, ledger.Path, created.ID, false)
			budget := admission.NewBudget(ledger)
			before, err := budget.Inspect(ctx, 7)
			if err != nil || before.ActiveCalls != 1 || before.UnknownUsage || before.ReservedTokens != 103 {
				t.Fatal("the fixture did not reserve an active call before crashing")
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			if err := admission.NewGateway(budget, admission.ProviderConfig{}).FinishTask(ctx, actor); err != nil {
				t.Fatalf("a confirmed stopped holder permanently fenced its interrupted call: %v", err)
			}
			after, err := budget.Inspect(ctx, 7)
			if err != nil || after.ID != before.ID || after.ActiveCalls != 0 || after.TaskID != "" || after.UsedTokens != 0 || after.ReservedTokens != 103 || !after.UnknownUsage || after.CostStatus != "unknown" {
				t.Fatalf("crash recovery discarded or invented model usage: %+v err=%v", after, err)
			}
		})
	}
}

func TestTaskCompletionRetainsUnconfirmedScopeProcessIdentity(t *testing.T) {
	for _, scenario := range []string{"legacy metadata", "different host", "invalid process", "invalid creation", "noncanonical creation", "invalid oversized PID", "invalid platform"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "fixture", "unconfirmed scope", "")
			if err != nil {
				t.Fatal(err)
			}
			actor, err := identity.Default().BindSession(created.ID)
			if err != nil {
				t.Fatal(err)
			}
			gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{})
			if err := gateway.Start(); err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			_, release, err := gateway.BindActor(actor, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, session.VerificationAdmissionLedgerID)
			if err != nil {
				t.Fatal(err)
			}
			var historical map[string]any
			if json.Unmarshal(snapshot.Events[0].Payload, &historical) != nil {
				t.Fatal("cannot read fixture lifecycle fact")
			}
			release()
			historical["scope_id"] = "historical-fixture-scope"
			process := historical["process"].(map[string]any)
			switch scenario {
			case "legacy metadata":
				delete(historical, "process")
			case "different host":
				process["host"] = strings.Repeat("0", 64)
			case "invalid process":
				process["pid"] = 0
			case "invalid oversized PID":
				process["pid"] = uint64(1) << 32
			case "invalid platform":
				process["platform"] = "unknown"
			case "invalid creation":
				process["started"] = "not-a-process-start"
			case "noncanonical creation":
				started := process["started"].(string)
				if runtime.GOOS == "windows" {
					process["started"] = "0" + started
				} else {
					parts := strings.Split(started, ":")
					process["started"] = parts[0] + ":0" + parts[1]
				}
			}
			events, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Append(ctx, session.VerificationAdmissionLedgerID, int64(len(events)), session.VerificationAdmissionScopeBoundEventType, historical); err != nil {
				t.Fatal(err)
			}
			before, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
			if err != nil {
				t.Fatal(err)
			}
			err = gateway.FinishTask(ctx, actor)
			if strings.HasPrefix(scenario, "invalid") || scenario == "noncanonical creation" {
				if !errors.Is(err, session.ErrEventIntegrity) {
					t.Fatalf("invalid process identity did not fail closed: %v", err)
				}
			} else if !errors.Is(err, admission.ErrTaskBusy) {
				t.Fatalf("unconfirmed historical holder was released: %v", err)
			}
			after, err := ledger.Events(ctx, session.VerificationAdmissionLedgerID)
			if err != nil || len(after) != len(before) || after[len(after)-1].Checksum != before[len(before)-1].Checksum {
				t.Fatal("unconfirmed process identity changed retained lifecycle or allowance facts")
			}
		})
	}
}

func TestScopeRejectsMismatchedProcPIDNamespace(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getenv("CODE_AGENT_RUN_PROC_NAMESPACE_TEST") != "1" {
		t.Skip("opt-in controlled Linux namespace fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "unshare", "--pid", "--fork", os.Args[0], "-test.run=^TestScopeProcNamespaceHelper$", "-test.v")
	command.Env = append(os.Environ(), "CODE_AGENT_SCOPE_TEST_NS_EXPECT_UNKNOWN=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("controlled namespace proof: %v\n%s", err, output)
	}
}

func TestScopeProcNamespaceHelper(t *testing.T) {
	if os.Getenv("CODE_AGENT_SCOPE_TEST_NS_EXPECT_UNKNOWN") != "1" {
		t.Skip("controlled namespace subprocess helper")
	}
	view, err := os.Readlink("/proc/self")
	if err != nil || view == strconv.Itoa(os.Getpid()) {
		t.Fatal("the fixture did not create a mismatched proc PID view")
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	multiple := false
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "NSpid:") {
			multiple = len(strings.Fields(line)) > 2
		}
	}
	if !multiple {
		t.Fatal("the fixture did not expose the ancestor proc PID namespace")
	}
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	created, err := session.NewWorkbench(ledger, nil).Create(ctx, 7, "fixture", "namespace visibility", "")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	budget := admission.NewBudget(ledger)
	gateway := admission.NewGateway(budget, admission.ProviderConfig{})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	_, release, err := gateway.BindActor(actor, created.ID)
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("mismatched proc PID visibility issued a model scope")
	}
	batch, err := budget.Inspect(ctx, 7)
	if err != nil || batch.ID != "" || batch.UsedTokens != 0 || batch.ReservedTokens != 0 {
		t.Fatal("namespace refusal created or changed an allowance")
	}
}

func TestModelScopeProcessHelper(t *testing.T) {
	path := os.Getenv("CODE_AGENT_SCOPE_TEST_DB")
	if path == "" {
		t.Skip("test-owned process helper")
	}
	ctx := context.Background()
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	actor, err := identity.Default().BindSession(os.Getenv("CODE_AGENT_SCOPE_TEST_SESSION"))
	if err != nil {
		t.Fatal(err)
	}
	var releaseDuringCall func()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scenario := os.Getenv("CODE_AGENT_SCOPE_TEST_CRASH_WINDOW"); scenario != "" {
			if scenario == "released" {
				releaseDuringCall()
			}
			fmt.Println("SCOPE_READY")
			<-r.Context().Done()
			return
		}
		if os.Getenv("CODE_AGENT_SCOPE_TEST_USAGE_UNKNOWN") == "1" {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"unattributed"}}]}`)
			return
		}
		fmt.Fprint(w, `{"usage":{"prompt_tokens":8,"completion_tokens":3}}`)
	}))
	defer upstream.Close()
	gateway := admission.NewGateway(admission.NewBudget(ledger), admission.ProviderConfig{Protocol: "openai", BaseURL: upstream.URL,
		Model: "fixture-model", APIKey: "fixture-only", InputLimit: 100, OutputLimit: 3})
	if err := gateway.Start(); err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	binding, release, err := gateway.BindActor(actor, actor.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	releaseDuringCall = release
	result, err := gateway.Invoke(ctx, &pb.ModelCallRequest{Capability: binding.Capability, CallId: "scope-fixture-call",
		PayloadJson: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"fixture"}]}`)})
	expected := ""
	if os.Getenv("CODE_AGENT_SCOPE_TEST_USAGE_UNKNOWN") == "1" {
		expected = "provider_usage_unknown"
	}
	if err != nil || result.ErrorCode != expected {
		t.Fatal("scope fixture call did not settle")
	}
	fmt.Println("SCOPE_READY")
	_, _ = io.Copy(io.Discard, os.Stdin)
}
