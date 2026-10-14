package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/session"
	"code-agent/internal/worktree"
)

type productApplicationCrash struct {
	Database, Repository, Storage, SessionID, ProposalID, Phase string
	Owner                                                       uint
	Seq                                                         int64
	Index                                                       int
}

// Only the public Ledger boundary is fault-injected; native application code is unchanged.
type productApplicationCrashLedger struct {
	session.EventLog
	input productApplicationCrash
}

func (l productApplicationCrashLedger) Append(ctx context.Context, id string, seq int64, kind string, payload any) (session.Event, error) {
	var item struct {
		Index int `json:"index"`
	}
	data, _ := json.Marshal(payload)
	_ = json.Unmarshal(data, &item)
	phase := l.input.Phase
	matches := strings.HasPrefix(phase, "parent-intent-") && kind == "workspace/application-intent" ||
		strings.HasPrefix(phase, "parent-outcome-") && kind == "workspace/application-outcome" ||
		phase == "item-intent-after" && kind == "workspace/application-item-intent" && item.Index == l.input.Index ||
		strings.HasPrefix(phase, "item-outcome-") && kind == "workspace/application-item-outcome" && item.Index == l.input.Index
	pause := func() { fmt.Println("APPLICATION_EFFECT_READY"); select {} }
	if matches && strings.HasSuffix(phase, "before") {
		pause()
	}
	event, err := l.EventLog.Append(ctx, id, seq, kind, payload)
	if err == nil && matches && strings.HasSuffix(phase, "after") {
		pause()
	}
	return event, err
}

func TestProductionApplicationCrashHelper(t *testing.T) {
	data := os.Getenv("CODE_AGENT_PRODUCT_APPLICATION_CRASH")
	if data == "" {
		t.Skip("owned subprocess fault injector")
	}
	var input productApplicationCrash
	if json.Unmarshal([]byte(data), &input) != nil {
		t.Fatal("invalid helper input")
	}
	ledger, err := session.OpenSQLiteEventLog(input.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	tasks := session.NewTaskWorkspaces(session.NewWorkbench(productApplicationCrashLedger{ledger, input}, nil), func(context.Context) (uint, error) { return input.Owner, nil }, input.Repository, input.Storage)
	_, err = tasks.DecideProposal(context.Background(), input.Owner, input.SessionID, input.Seq, "crashed-product-application", input.ProposalID, "approve")
	t.Fatalf("fault producer unexpectedly returned: %v", err)
}

func testProductionApplicationRecovery(t *testing.T, fixture *localCoreProcess, repository, storage string, created session.SessionView, prepared session.TaskWorkspaceView, baseline worktree.Baseline) {
	t.Helper()
	artifact := os.Getenv("CODE_AGENT_LOCAL_WORKSPACE_ARTIFACT")
	if artifact == "" {
		t.Fatal("immutable recovery artifact destination is required")
	}
	path := "/api/v1/sessions/" + created.ID
	request := func(t *testing.T, method, route string, body, target any, expected int) int {
		t.Helper()
		data, _ := json.Marshal(body)
		req, err := http.NewRequest(method, fixture.base+route, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := fixture.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if expected != 0 && response.StatusCode != expected || expected == 0 && response.StatusCode != 200 && response.StatusCode != 409 {
			t.Fatalf("%s %s = %d, want %d", method, route, response.StatusCode, expected)
		}
		if target != nil && response.StatusCode == 200 && json.NewDecoder(response.Body).Decode(target) != nil {
			t.Fatal("invalid product response")
		}
		return response.StatusCode
	}
	var current struct{ Data session.TaskWorkspaceView }
	request(t, http.MethodGet, path+"/task-workspace", nil, &current, 200)
	proposalID := current.Data.Proposal.ID
	fixture.stop(t)
	database := filepath.Join(fixture.dir, "sessions.sqlite")
	ledger, err := session.OpenSQLiteEventLog(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ReadVerifiedSnapshot(context.Background(), ledger, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Close(); err != nil {
		t.Fatal(err)
	}
	initialLedger, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	initialFiles := map[string][]byte{}
	for _, name := range []string{"calc.go", "empty.txt", "old.txt"} {
		initialFiles[name], err = os.ReadFile(filepath.Join(repository, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	indexPath := filepath.Join(repository, ".git", "index")
	initialIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	resetOwnedCase := func(t *testing.T) {
		t.Helper()
		fixture.stop(t)
		for _, name := range []string{"nested/new.txt", "moved.txt"} {
			if err := os.Remove(filepath.Join(repository, filepath.FromSlash(name))); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		for name, content := range initialFiles {
			if err := os.WriteFile(filepath.Join(repository, name), content, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(indexPath, initialIndex, 0600); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := os.Remove(database + suffix); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(database, initialLedger, 0600); err != nil {
			t.Fatal(err)
		}
		actual, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
		if err != nil || actual.Checksum != baseline.Checksum {
			t.Fatal("owned test case did not restore the frozen source")
		}
	}
	retain := func(t *testing.T, label string, view session.TaskWorkspaceView) string {
		t.Helper()
		ledger, err := session.OpenSQLiteEventLog(database)
		if err != nil {
			t.Fatal(err)
		}
		defer ledger.Close()
		snapshot, err := session.ReadVerifiedSnapshot(context.Background(), ledger, view.Proposal.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		name := artifact + "." + label + ".json"
		file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		actual, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
		if err != nil || actual.IndexSHA256 != baseline.IndexSHA256 || actual.HeadCommit != baseline.HeadCommit {
			t.Fatal("recovery changed index or HEAD")
		}
		if json.NewEncoder(file).Encode(map[string]any{"phase": label, "planned_items": len(view.Proposal.Files), "view": view, "events": snapshot.Events, "original_after": actual, "paid_model_calls": 0}) != nil {
			t.Fatal("could not retain recovery facts")
		}
		return name
	}
	for _, phase := range []string{"parent-intent-before", "parent-intent-after", "item-intent-after", "item-outcome-before", "item-outcome-after", "parent-outcome-before", "parent-outcome-after"} {
		count := 1
		if strings.HasPrefix(phase, "item-") {
			count = 4
		}
		for index := 0; index < count; index++ {
			label := fmt.Sprintf("%s-%d", phase, index)
			t.Run(label, func(t *testing.T) {
				resetOwnedCase(t)
				input, _ := json.Marshal(productApplicationCrash{Database: database, Repository: repository, Storage: storage, SessionID: created.ID, ProposalID: proposalID, Owner: created.UserID, Seq: int64(current.Data.EventCount), Phase: phase, Index: index})
				command := exec.Command(os.Args[0], "-test.run=^TestProductionApplicationCrashHelper$", "-test.timeout=60s")
				command.Env = append(os.Environ(), "CODE_AGENT_PRODUCT_APPLICATION_CRASH="+string(input))
				output, err := command.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if command.ProcessState == nil {
						_ = command.Process.Kill()
						_ = command.Wait()
					}
				}()
				ready := make(chan bool, 1)
				go func() {
					scanner := bufio.NewScanner(output)
					for scanner.Scan() {
						if strings.Contains(scanner.Text(), "APPLICATION_EFFECT_READY") {
							ready <- true
							return
						}
					}
					ready <- false
				}()
				select {
				case ok := <-ready:
					if !ok {
						t.Fatal("producer missed the frozen effect boundary")
					}
				case <-time.After(30 * time.Second):
					t.Fatal("effect boundary timed out")
				}
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = command.Wait()
				fixture.start(t)
				var observed struct{ Data session.TaskWorkspaceView }
				request(t, http.MethodGet, path+"/task-workspace", nil, &observed, 200)
				if observed.Data.Proposal == nil || observed.Data.Proposal.ID != proposalID || len(observed.Data.Proposal.Files) != 4 {
					t.Fatal("restart lost the isolated proposal or denominator")
				}
				if phase == "parent-intent-before" && observed.Data.Application != nil {
					t.Fatal("unrecorded application became an applied result")
				}
				retain(t, label+"-before-reconcile", observed.Data)
				if phase != "parent-intent-before" {
					for attempt := 0; attempt < 3; attempt++ {
						request(t, http.MethodGet, path+"/task-workspace", nil, &observed, 200)
						if request(t, http.MethodPost, path+"/task-workspace/proposal/reconcile", map[string]any{"expectedSeq": observed.Data.EventCount, "requestId": "inspect-product", "applicationRequestId": "crashed-product-application"}, &observed, 0) == 200 {
							break
						}
						retain(t, fmt.Sprintf("%s-cas-refusal-%d", label, attempt), observed.Data)
						t.Logf("read-only recovery CAS refusal %d; refreshing the same request identity", attempt+1)
						if attempt == 2 {
							t.Fatal("read-only recovery cursor did not settle")
						}
					}
					applied := 0
					if strings.HasPrefix(phase, "item-") {
						applied = index
						if strings.HasPrefix(phase, "item-outcome-") {
							applied++
						}
					}
					if strings.HasPrefix(phase, "parent-outcome-") {
						applied = 4
					}
					for i, item := range observed.Data.Application.Items {
						want := "not_started"
						if i < applied {
							want = "applied"
						} else if phase == "item-intent-after" && i == index {
							want = "not_applied"
						}
						if item.State != want {
							t.Fatalf("item %d = %s want %s", i, item.State, want)
						}
					}
					if !observed.Data.Application.Reconciled || len(observed.Data.Application.Items) != 4 {
						t.Fatal("recovery lost item proofs")
					}
					beforeReplay, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					request(t, http.MethodPost, path+"/task-workspace/proposal/decision", map[string]any{"expectedSeq": 0, "requestId": "crashed-product-application", "proposalId": proposalID, "decision": "approve"}, &observed, 200)
					afterReplay, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
					if err != nil || afterReplay.Checksum != beforeReplay.Checksum {
						t.Fatal("same request repeated a native effect")
					}
				}
				name := retain(t, label, observed.Data)
				browser := exec.Command("node", "tests/e2e/application_recovery_browser.mjs")
				browser.Dir = e2ERepositoryRoot(t)
				browser.Env = append(os.Environ(), "CODE_AGENT_DECISION_URL="+fixture.base, "CODE_AGENT_DECISION_PASSWORD="+fixture.password, "CODE_AGENT_DECISION_SESSION="+created.ID, "CODE_AGENT_DECISION_PROPOSAL="+proposalID, "CODE_AGENT_RECOVERY_VIEW="+name, "CODE_AGENT_DECISION_ARTIFACT="+name+".browser.json")
				if output, err := browser.CombinedOutput(); err != nil {
					t.Fatalf("product recovery browser: %v: %s", err, output)
				}
				request(t, http.MethodGet, path+"/task-workspace", nil, &observed, 200)
				retain(t, label+"-after-browser", observed.Data)
				if phase == "item-outcome-after" && index == 0 {
					request(t, http.MethodPost, path+"/task/finish", map[string]any{}, nil, 200)
					var freshSession struct{ Data session.SessionView }
					request(t, http.MethodPost, "/api/v1/sessions", map[string]any{"projectName": "approved-repo", "title": "review remaining work", "workingDir": repository}, &freshSession, 200)
					freshPath := "/api/v1/sessions/" + freshSession.Data.ID
					var fresh struct{ Data session.TaskWorkspaceView }
					request(t, http.MethodPost, freshPath+"/task-workspace/prepare", map[string]any{"expectedSeq": freshSession.Data.EventCount, "requestId": "fresh-prepare"}, &fresh, 200)
					actual, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
					if err != nil || fresh.Data.Baseline.Checksum == baseline.Checksum || fresh.Data.Baseline.Checksum != actual.Checksum {
						t.Fatal("fresh task reused the old baseline")
					}
					request(t, http.MethodPost, freshPath+"/task-workspace/test", map[string]any{"expectedSeq": fresh.Data.EventCount, "requestId": "fresh-test"}, &fresh, 200)
					if fresh.Data.Test == nil || fresh.Data.Test.State != "passed" || !fresh.Data.Test.Stopped {
						t.Fatal("fresh baseline did not run real tests")
					}
					deadline := time.Now().Add(20 * time.Second)
					for {
						var health struct{ Continuation struct{ Attached bool } }
						request(t, http.MethodGet, "/healthz", nil, &health, 200)
						if health.Continuation.Attached {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("fresh task continuation did not attach")
						}
						time.Sleep(50 * time.Millisecond)
					}
					request(t, http.MethodPost, freshPath+"/messages", map[string]any{"expectedSeq": fresh.Data.EventCount, "requestId": "fresh-work", "content": "review the remaining three changes"}, nil, 202)
					deadline = time.Now().Add(180 * time.Second)
					approved := map[string]bool{}
					for {
						var events struct{ Data []session.EventView }
						request(t, http.MethodGet, freshPath+"/events", nil, &events, 200)
						for _, event := range events.Data {
							if event.Type == "approval/pending" && event.Approval != nil && !approved[event.ID] {
								request(t, http.MethodPost, freshPath+"/approvals/"+event.Approval.RunID+"/"+event.Approval.ToolCallID, map[string]any{"decision": "approved", "pendingEventId": event.ID, "pendingSeq": event.Seq}, nil, 200)
								approved[event.ID] = true
							}
						}
						var running struct{ Data session.SessionView }
						request(t, http.MethodGet, freshPath, nil, &running, 200)
						if running.Data.Run != nil && running.Data.Run.Status == session.RunCompleted {
							break
						}
						if running.Data.Run != nil && running.Data.Run.Status == session.RunFailed {
							t.Fatal("fresh task failed")
						}
						if time.Now().After(deadline) {
							t.Fatal("fresh task timed out")
						}
						time.Sleep(50 * time.Millisecond)
					}
					request(t, http.MethodGet, freshPath+"/task-workspace", nil, &fresh, 200)
					for attempt := 0; attempt < 3; attempt++ {
						request(t, http.MethodGet, freshPath+"/task-workspace", nil, &fresh, 200)
						if request(t, http.MethodPost, freshPath+"/task-workspace/proposal", map[string]any{"expectedSeq": fresh.Data.EventCount, "requestId": "fresh-proposal"}, &fresh, 0) == 200 {
							break
						}
						if attempt == 2 {
							t.Fatal("fresh proposal cursor did not settle")
						}
					}
					if fresh.Data.Proposal == nil || fresh.Data.Proposal.State != "ready" || fresh.Data.Proposal.Test.State != "passed" || len(fresh.Data.Proposal.Files) != 3 {
						t.Fatal("fresh proposal was not tested against the new baseline")
					}
					freshArtifact := retain(t, "fresh-review", fresh.Data)
					approve := exec.Command("node", "tests/e2e/application_recovery_browser.mjs")
					approve.Dir = e2ERepositoryRoot(t)
					approve.Env = append(os.Environ(), "CODE_AGENT_DECISION_URL="+fixture.base, "CODE_AGENT_DECISION_PASSWORD="+fixture.password, "CODE_AGENT_DECISION_SESSION="+freshSession.Data.ID, "CODE_AGENT_DECISION_PROPOSAL="+fresh.Data.Proposal.ID, "CODE_AGENT_RECOVERY_VIEW="+freshArtifact, "CODE_AGENT_RECOVERY_APPROVE=1", "CODE_AGENT_DECISION_ARTIFACT="+freshArtifact+".browser.json")
					if output, err := approve.CombinedOutput(); err != nil {
						t.Fatalf("new approval: %v %s", err, output)
					}
					request(t, http.MethodGet, freshPath+"/task-workspace", nil, &fresh, 200)
					retain(t, "fresh-applied", fresh.Data)
					request(t, http.MethodGet, path+"/task-workspace", nil, &observed, 200)
					if observed.Data.Proposal.ID != proposalID || observed.Data.Application.State != "partial" {
						t.Fatal("new approval rewrote the old partial history")
					}
					retain(t, "old-proposal-retained", observed.Data)
					request(t, http.MethodPost, freshPath+"/task/finish", map[string]any{}, nil, 200)
				}
			})
		}
	}
	// Concurrent source changes are exercised through the unmodified product approval route.
	for _, change := range []string{"content", "existence", "index"} {
		t.Run("concurrent-"+change, func(t *testing.T) {
			resetOwnedCase(t)
			if change == "existence" {
				if err := os.Remove(filepath.Join(repository, "empty.txt")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(repository, "calc.go"), []byte("user newer source\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if change == "index" {
					if output, err := exec.Command("git", "-C", repository, "add", "calc.go").CombinedOutput(); err != nil {
						t.Fatalf("owned index: %v %s", err, output)
					}
				}
			}
			before, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			fixture.start(t)
			var observed struct{ Data session.TaskWorkspaceView }
			request(t, http.MethodGet, path+"/task-workspace", nil, &observed, 200)
			request(t, http.MethodPost, path+"/task-workspace/proposal/decision", map[string]any{"expectedSeq": observed.Data.EventCount, "requestId": "concurrent-" + change, "proposalId": proposalID, "decision": "approve"}, nil, 409)
			after, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
			if err != nil || after.Checksum != before.Checksum {
				t.Fatal("refused approval overwrote the user's current state")
			}
			request(t, http.MethodGet, path+"/task-workspace", nil, &observed, 200)
			ledger, err := session.OpenSQLiteEventLog(database)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := session.ReadVerifiedSnapshot(context.Background(), ledger, created.ID)
			_ = ledger.Close()
			if err != nil {
				t.Fatal(err)
			}
			name := artifact + ".concurrent-" + change + ".json"
			file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if json.NewEncoder(file).Encode(map[string]any{"view": observed.Data, "events": snapshot.Events, "before": before, "after": after, "status": "refused_without_effects"}) != nil {
				t.Fatal("could not retain the concurrent case")
			}
			_ = file.Close()
			if change == "content" {
				browser := exec.Command("node", "tests/e2e/application_recovery_browser.mjs")
				browser.Dir = e2ERepositoryRoot(t)
				browser.Env = append(os.Environ(), "CODE_AGENT_DECISION_URL="+fixture.base, "CODE_AGENT_DECISION_PASSWORD="+fixture.password, "CODE_AGENT_DECISION_SESSION="+created.ID, "CODE_AGENT_DECISION_PROPOSAL="+proposalID, "CODE_AGENT_RECOVERY_VIEW="+name, "CODE_AGENT_RECOVERY_CONFLICT=1", "CODE_AGENT_DECISION_ARTIFACT="+name+".browser.json")
				if output, err := browser.CombinedOutput(); err != nil {
					t.Fatalf("product conflict browser: %v %s", err, output)
				}
				final, err := worktree.NewManager(repository, "HEAD").CaptureBaseline(context.Background())
				if err != nil || final.Checksum != before.Checksum {
					t.Fatal("browser refusal changed the original source")
				}
			}
		})
	}
	resetOwnedCase(t)
	fixture.start(t)
}
