package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"code-agent/internal/admission"
	"code-agent/internal/sandbox"
	"code-agent/internal/session"
)

type dependencyTransport func(*http.Request) (*http.Response, error)

func (transport dependencyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestTaskDependencyRetainsRefusalsAndUnknownAdmissionAfterRestart(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows task preparation contract")
	}
	for _, scenario := range []string{"wrong hash", "redirect", "wrong length", "truncated transfer", "interrupted transfer", "unknown admission and changed source"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			repo, storage := t.TempDir(), t.TempDir()
			for _, args := range [][]string{{"init"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
				if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("Git fixture: %s %v", output, err)
				}
			}
			if err := os.WriteFile(filepath.Join(repo, "setup.py"), []byte("# Python project fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
				if err := exec.Command("git", append([]string{"-C", repo}, args...)...).Run(); err != nil {
					t.Fatal(err)
				}
			}
			ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			workbench := session.NewWorkbench(ledger, nil)
			created, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "Python dependency", "", repo)
			if err != nil {
				t.Fatal(err)
			}
			owner := func(context.Context) (uint, error) { return 7, nil }
			tasks := session.NewTaskWorkspaces(workbench, owner, repo, storage)
			if prepared, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare"); err != nil || prepared.State != "prepared" {
				t.Fatalf("prepare: %+v %v", prepared, err)
			}
			requests := 0
			budget := admission.NewBudget(ledger)
			client := &http.Client{Transport: dependencyTransport(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Method != http.MethodGet || request.URL.String() != "https://files.pythonhosted.org/packages/4f/ef/c66110d46fb800dda0bf33164182dfadabe26a90e4476844d502a23dca8e/pytz-2026.5-py2.py3-none-any.whl" {
					t.Error("unapproved dependency request")
				}
				status, length, body := 200, int64(506342), strings.Repeat("x", 506342)
				header := http.Header{}
				switch scenario {
				case "redirect":
					status = http.StatusFound
					header.Set("Location", "https://unapproved.example/extra.whl")
				case "wrong length":
					length, body = 1, "x"
				case "truncated transfer":
					body = "partial"
				case "interrupted transfer":
					return nil, context.Canceled
				case "unknown admission and changed source":
					batch, err := budget.Inspect(ctx, 7)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := budget.Reserve(ctx, 7, batch.TaskID, "unreconciled-call", 1); err != nil {
						t.Fatal(err)
					}
					if _, err := budget.MarkUnknown(ctx, 7, "unreconciled-call"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(repo, "setup.py"), []byte("# concurrent source change\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return &http.Response{StatusCode: status, ContentLength: length, Body: io.NopCloser(strings.NewReader(body)), Header: header}, nil
			})}
			profile := session.TaskBaselineRuntime{Toolchain: "python", DependencyClient: client,
				Runner: sandbox.NewOpenSandboxRunner(sandbox.Config{TrustRoot: storage, Toolchain: "python"}),
				Acquire: func(ctx context.Context, owner uint, id string) error {
					_, err := budget.AcquireTask(ctx, owner, id)
					return err
				},
				Finish: func(ctx context.Context, owner uint, id string) error {
					_, err := budget.FinishTask(ctx, owner, id)
					return err
				},
			}
			tasks.WithBaselineRuntime(profile)
			if _, err := tasks.PrepareDependency(ctx, 8, created.ID, 3, "dependency"); !errors.Is(err, session.ErrSessionNotFound) {
				t.Fatal("foreign owner admitted")
			}
			if _, err := tasks.PrepareDependency(ctx, 7, created.ID, 2, "dependency"); !errors.Is(err, session.ErrSequenceConflict) {
				t.Fatal("stale cursor admitted")
			}
			view, err := tasks.PrepareDependency(ctx, 7, created.ID, 3, "dependency")
			wantWorkspace, wantDependency := "prepared", "blocked"
			if scenario == "unknown admission and changed source" {
				wantDependency = "unknown"
			}
			if err != nil || view.State != wantWorkspace || view.EventCount != 5 {
				t.Fatalf("dependency failure was lost: %+v %v", view, err)
			}
			encoded, _ := json.Marshal(view)
			var decoded struct {
				Dependency struct{ State, SHA256, OperationID string }
			}
			if json.Unmarshal(encoded, &decoded) != nil || decoded.Dependency.State != wantDependency || decoded.Dependency.SHA256 != "e658af3757f9e26a9d25dd2aff38335acd92bc9104f890a894b2c1ba28311b03" || len(decoded.Dependency.OperationID) != 64 {
				t.Fatalf("dependency provenance missing: %s", encoded)
			}
			reopened := session.NewTaskWorkspaces(session.NewWorkbench(ledger, nil), owner, repo, storage).WithBaselineRuntime(profile)
			again, err := reopened.PrepareDependency(ctx, 7, created.ID, 3, "dependency")
			againJSON, _ := json.Marshal(again)
			if err != nil || string(againJSON) != string(encoded) || requests != 1 {
				t.Fatalf("restart retried dependency: %+v requests=%d err=%v", again, requests, err)
			}
			if _, err := reopened.PrepareDependency(ctx, 7, created.ID, 5, "different"); !errors.Is(err, session.ErrSessionStateConflict) {
				t.Fatal("new request replaced failed preparation")
			}
			batch, err := budget.Inspect(ctx, 7)
			if scenario == "unknown admission and changed source" {
				if err != nil || batch.TaskID != decoded.Dependency.OperationID || batch.ReservedTokens != 1 || !batch.UnknownUsage {
					t.Fatalf("unknown admission was released: %+v %v", batch, err)
				}
				return
			}
			if err != nil || batch.TaskID != "" || batch.UsedTokens != 0 || batch.ReservedTokens != 0 {
				t.Fatalf("dependency failed to release stopped slot: %+v %v", batch, err)
			}
		})
	}
}
