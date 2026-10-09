package admission_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/admission"
	"code-agent/internal/session"
)

type processResult struct {
	Batch     admission.Batch
	Admitted  bool
	Rejection string
}

func budgetProcess(ctx context.Context, path, action, call, gate string) (*exec.Cmd, *bytes.Buffer) {
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBudgetProcessHelper$", "-test.v")
	command.Env = append(os.Environ(), "CODE_AGENT_BUDGET_TEST_DB="+path,
		"CODE_AGENT_BUDGET_TEST_ACTION="+action, "CODE_AGENT_BUDGET_TEST_CALL="+call,
		"CODE_AGENT_BUDGET_TEST_GATE="+gate)
	output := &bytes.Buffer{}
	command.Stdout, command.Stderr = output, output
	return command, output
}

func resultFromProcess(t *testing.T, output *bytes.Buffer) processResult {
	t.Helper()
	for _, line := range strings.Split(output.String(), "\n") {
		if body, ok := strings.CutPrefix(line, "BUDGET_RESULT:"); ok {
			var result processResult
			if err := json.Unmarshal([]byte(body), &result); err != nil {
				t.Fatal(err)
			}
			return result
		}
	}
	t.Fatalf("no budget process result: %s", output)
	return processResult{}
}

func TestBudgetAcrossIndependentProcesses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "ledger.sqlite")
	first, firstOutput := budgetProcess(ctx, path, "seed", "call-a", "")
	if err := first.Run(); err != nil {
		t.Fatalf("first process: %v\n%s", err, firstOutput)
	}
	seed := resultFromProcess(t, firstOutput)
	second, secondOutput := budgetProcess(ctx, path, "restore", "call-b", "")
	if err := second.Run(); err != nil {
		t.Fatalf("second process: %v\n%s", err, secondOutput)
	}
	recovered := resultFromProcess(t, secondOutput)
	if recovered.Batch.ID != seed.Batch.ID || recovered.Batch.UsedTokens != 20_000_000 || recovered.Batch.ReservedTokens != 0 || recovered.Rejection != "budget_exhausted" {
		t.Fatal("independent process reset the allowance or admitted an over-budget call")
	}
}

func TestConcurrentProcessesCannotOverspend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	path := filepath.Join(root, "ledger.sqlite")
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admission.NewBudget(ledger).Open(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(root, "start")
	left, leftOutput := budgetProcess(ctx, path, "reserve", "left", gate)
	right, rightOutput := budgetProcess(ctx, path, "reserve", "right", gate)
	if err := left.Start(); err != nil {
		t.Fatal(err)
	}
	if err := right.Start(); err != nil {
		cancel()
		_ = left.Wait()
		t.Fatal(err)
	}
	for {
		_, leftErr := os.Stat(gate + ".left")
		_, rightErr := os.Stat(gate + ".right")
		if leftErr == nil && rightErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			_ = left.Wait()
			_ = right.Wait()
			t.Fatal("both child processes did not reach the reservation barrier")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := os.WriteFile(gate, []byte("start"), 0o600); err != nil {
		cancel()
		_ = left.Wait()
		_ = right.Wait()
		t.Fatal(err)
	}
	leftErr, rightErr := left.Wait(), right.Wait()
	if leftErr != nil || rightErr != nil {
		t.Fatalf("reservation processes: %v / %v\n%s\n%s", leftErr, rightErr, leftOutput, rightOutput)
	}
	admitted, rejected := 0, 0
	for _, output := range []*bytes.Buffer{leftOutput, rightOutput} {
		result := resultFromProcess(t, output)
		if result.Admitted {
			admitted++
		}
		if result.Rejection == "budget_exhausted" {
			rejected++
		}
	}
	if admitted != 1 || rejected != 1 {
		t.Fatalf("admitted %d, budget rejections %d; want 1 each", admitted, rejected)
	}
	ledger, err = session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	batch, err := admission.NewBudget(ledger).Open(ctx, 7)
	if err != nil || batch.ReservedTokens != 60_000_000 || batch.UsedTokens != 0 {
		t.Fatal("concurrent reservations were lost or exceeded the allowance")
	}
}

func TestBudgetProcessHelper(t *testing.T) {
	path := os.Getenv("CODE_AGENT_BUDGET_TEST_DB")
	if path == "" {
		t.Skip("subprocess helper; no model calls")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ledger, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	budget := admission.NewBudget(ledger)
	call := os.Getenv("CODE_AGENT_BUDGET_TEST_CALL")
	if gate := os.Getenv("CODE_AGENT_BUDGET_TEST_GATE"); gate != "" {
		if err := os.WriteFile(gate+"."+call, []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := os.Stat(gate); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("reservation barrier timed out")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	result := processResult{}
	switch os.Getenv("CODE_AGENT_BUDGET_TEST_ACTION") {
	case "seed":
		if _, err := budget.Reserve(ctx, 7, "task-a", call, 60_000_000); err != nil {
			t.Fatal(err)
		}
		result.Batch, err = budget.Settle(ctx, 7, call, 20_000_000)
	case "restore", "reserve":
		tokens := int64(60_000_000)
		if os.Getenv("CODE_AGENT_BUDGET_TEST_ACTION") == "restore" {
			tokens = 90_000_000
		}
		result.Batch, err = budget.Reserve(ctx, 7, "task-a", call, tokens)
		if errors.Is(err, admission.ErrBudgetExhausted) {
			result.Rejection, err = "budget_exhausted", nil
		} else if err == nil {
			result.Admitted = true
		}
	default:
		t.Fatal("unknown test action")
	}
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.Write(append([]byte("BUDGET_RESULT:"), append(body, '\n')...))
}
