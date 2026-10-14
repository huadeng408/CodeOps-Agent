package session

import (
	"code-agent/internal/tools"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readyLifecycleProposal(t *testing.T, ledger EventLog) (*TaskWorkspaces, EventLog, string, string, TaskWorkspaceView) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first file lifecycle")
	}
	ctx := context.Background()
	repo, storage := taskWorkspaceRepository(t), t.TempDir()
	for path, content := range map[string]string{"empty.txt": "", "old.txt": "rename content"} {
		if err := os.WriteFile(filepath.Join(repo, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if ledger == nil {
		ledger = openWorkbenchTestLedger(t)
	}
	w := NewWorkbench(ledger, nil)
	created, err := w.CreateWithWorkingDir(ctx, 7, "repo", "lifecycle", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskWorkspaces(w, taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	view, err := tasks.Prepare(ctx, 7, created.ID, 1, "prepare-lifecycle")
	if err != nil || view.State != "prepared" {
		t.Fatalf("prepare: %+v %v", view, err)
	}
	executor := tools.NewExecutor(filepath.Join(storage, view.WorkspaceID))
	defer executor.Close()
	seq := int64(view.EventCount)
	appendFact := func(kind string, payload any) {
		t.Helper()
		if _, err := ledger.Append(ctx, created.ID, seq, kind, payload); err != nil {
			t.Fatal(err)
		}
		seq++
	}
	for index, request := range []tools.ToolRequest{
		{Name: "Write", Arguments: map[string]any{"path": "new.txt", "content": ""}},
		{Name: "Edit", Arguments: map[string]any{"path": "main.go", "old": "dirty", "new": "reviewed"}},
		{Name: "Delete", Arguments: map[string]any{"path": "empty.txt"}},
		{Name: "Rename", Arguments: map[string]any{"path": "old.txt", "destination": "moved.txt"}},
	} {
		callID := fmt.Sprintf("effect-%d", index)
		appendFact("tool/dispatched", map[string]any{"run_id": "fixture-run", "tool_call_id": callID, "tool_name": request.Name})
		result, err := executor.Execute(ctx, request)
		if err != nil || result.ExitCode != 0 || len(result.Changes) != 1 {
			t.Fatalf("%s: %+v %v", request.Name, result, err)
		}
		change := result.Changes[0]
		appendFact("code/modified", map[string]any{"run_id": "fixture-run", "tool_call_id": callID, "tool_name": request.Name, "operation": request.Name, "summary": "Go file effect", "path": change.Path, "destination": change.Destination, "before": change.Before, "after": change.After, "before_exists": *change.BeforeExists, "after_exists": *change.AfterExists, "before_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(change.Before))), "after_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(change.After))), "diff_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(change.Before+"\x00"+change.After)))})
		appendFact("tool/result", map[string]any{"run_id": "fixture-run", "tool_call_id": callID, "tool_name": request.Name, "exit_code": 0})
	}
	view, err = tasks.GenerateProposal(ctx, 7, created.ID, seq, "lifecycle-proposal")
	if err != nil || view.Proposal == nil || view.Proposal.State != "ready" || len(view.Proposal.Files) != 4 {
		t.Fatalf("proposal: %+v %v", view, err)
	}
	return tasks, ledger, repo, storage, view
}

func TestLifecycleProposalPreservesCreateEditDeleteAndRename(t *testing.T) {
	ctx := context.Background()
	tasks, _, repo, _, view := readyLifecycleProposal(t, nil)
	seen := map[string]bool{}
	for _, file := range view.Proposal.Files {
		seen[file.Operation] = true
		if file.Operation == "create" && (file.BeforeExists || !file.AfterExists || file.AfterSHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") {
			t.Fatal("empty creation lost existence")
		}
		if file.Operation == "delete" && (!file.BeforeExists || file.AfterExists) {
			t.Fatal("empty deletion lost existence")
		}
		if file.Operation == "rename" && (file.Path != "old.txt" || file.Destination != "moved.txt" || !strings.Contains(file.Diff, "a/old.txt") || !strings.Contains(file.Diff, "b/moved.txt")) {
			t.Fatal("rename preview lost a path")
		}
	}
	if len(seen) != 4 {
		t.Fatal("proposal lifecycle operations were collapsed")
	}
	applied, err := tasks.DecideProposal(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "apply-lifecycle", view.Proposal.ID, "approve")
	if err != nil || applied.Application == nil || applied.Application.State != "applied" || len(applied.Application.Items) != 4 {
		t.Fatalf("application: %+v %v", applied, err)
	}
	for _, item := range applied.Application.Items {
		if item.State != "applied" {
			t.Fatal("batch lost an item result")
		}
	}
	data, _ := os.ReadFile(filepath.Join(repo, "main.go"))
	if string(data) != "reviewed" {
		t.Fatal("batch edit not applied")
	}
	if data, err := os.ReadFile(filepath.Join(repo, "new.txt")); err != nil || len(data) != 0 {
		t.Fatal("new empty file was not applied")
	}
	if _, err := os.Stat(filepath.Join(repo, "empty.txt")); !os.IsNotExist(err) {
		t.Fatal("empty file was not deleted")
	}
	if _, err := os.Stat(filepath.Join(repo, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("rename source remained")
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "moved.txt")); string(data) != "rename content" {
		t.Fatal("rename target not applied")
	}
}

type refuseSecondItemIntent struct{ EventLog }

func TestLifecycleProposalReplacesADeletedOriginalRenameTarget(t *testing.T) {
	ctx := context.Background()
	tasks, ledger, repo, storage, view := readyLifecycleProposal(t, nil)
	executor := tools.NewExecutor(filepath.Join(storage, view.WorkspaceID))
	defer executor.Close()
	seq := int64(view.EventCount)
	for index, request := range []tools.ToolRequest{
		{Name: "Delete", Arguments: map[string]any{"path": "main.go"}},
		{Name: "Rename", Arguments: map[string]any{"path": "moved.txt", "destination": "main.go"}},
	} {
		result, err := executor.Execute(ctx, request)
		if err != nil || result.ExitCode != 0 || len(result.Changes) != 1 {
			t.Fatalf("replacement tool: %+v %v", result, err)
		}
		change, callID := result.Changes[0], fmt.Sprintf("replace-%d", index)
		for _, fact := range []struct {
			kind    string
			payload any
		}{
			{"tool/dispatched", map[string]any{"run_id": "replacement", "tool_call_id": callID, "tool_name": request.Name}},
			{"code/modified", map[string]any{"run_id": "replacement", "tool_call_id": callID, "tool_name": request.Name, "operation": request.Name, "summary": "Go file effect", "path": change.Path, "destination": change.Destination, "before": change.Before, "after": change.After, "before_exists": *change.BeforeExists, "after_exists": *change.AfterExists, "before_sha256": hashCodeState(change.Before), "after_sha256": hashCodeState(change.After), "diff_sha256": hashCodeTransition(change.Before, change.After)}},
			{"tool/result", map[string]any{"run_id": "replacement", "tool_call_id": callID, "tool_name": request.Name, "exit_code": 0}},
		} {
			if _, err := ledger.Append(ctx, view.Proposal.SessionID, seq, fact.kind, fact.payload); err != nil {
				t.Fatal(err)
			}
			seq++
		}
	}
	view, err := tasks.GenerateProposal(ctx, 7, view.Proposal.SessionID, seq, "replacement-proposal")
	if err != nil || view.Proposal == nil || view.Proposal.State != "ready" {
		t.Fatalf("replacement proposal: %+v %v", view, err)
	}
	operations := map[string]string{}
	for _, file := range view.Proposal.Files {
		operations[file.Path] = file.Operation
	}
	if operations["old.txt"] != "delete" || operations["main.go"] != "edit" {
		t.Fatalf("occupied original rename target lost its net effects: %v", operations)
	}
	view, err = tasks.DecideProposal(ctx, 7, view.Proposal.SessionID, int64(view.EventCount), "apply-replacement", view.Proposal.ID, "approve")
	if err != nil || view.Application == nil || view.Application.State != "applied" {
		t.Fatalf("replacement application: %+v %v", view, err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil || string(data) != "rename content" {
		t.Fatal("replacement target content was not applied")
	}
	if _, err := os.Stat(filepath.Join(repo, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("replacement source was not deleted")
	}
}

func (l refuseSecondItemIntent) Append(ctx context.Context, id string, seq int64, kind string, payload any) (Event, error) {
	if kind == "workspace/application-item-intent" {
		data, _ := json.Marshal(payload)
		var item struct {
			Index int `json:"index"`
		}
		if json.Unmarshal(data, &item) != nil {
			return Event{}, errors.New("invalid fixture item")
		}
		if item.Index == 1 {
			return Event{}, ErrSequenceConflict
		}
	}
	return l.EventLog.Append(ctx, id, seq, kind, payload)
}

func TestLifecyclePartialApplicationRetainsItemsAndNeverReplaysWrites(t *testing.T) {
	ctx := context.Background()
	ledger := openWorkbenchTestLedger(t)
	tasks, _, repo, storage, view := readyLifecycleProposal(t, refuseSecondItemIntent{ledger})
	sid := view.Proposal.SessionID
	partial, err := tasks.DecideProposal(ctx, 7, sid, int64(view.EventCount), "partial", view.Proposal.ID, "approve")
	if err != nil || partial.Application == nil || partial.Application.State != "partial" || len(partial.Application.Items) != 4 {
		t.Fatalf("partial: %+v %v", partial, err)
	}
	if partial.Application.Items[0].Path != "empty.txt" || partial.Application.Items[0].State != "applied" {
		t.Fatal("known first deletion disappeared")
	}
	for _, item := range partial.Application.Items[1:] {
		if item.State != "not_started" {
			t.Fatal("unattempted item was hidden or reported successful")
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "empty.txt"), []byte("later user file"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened := NewTaskWorkspaces(NewWorkbench(ledger, nil), taskWorkspaceOwner, repo, storage).WithBaselineRuntime(applicationRuntimeProfile())
	again, err := reopened.DecideProposal(ctx, 7, sid, 0, "partial", view.Proposal.ID, "approve")
	data, _ := os.ReadFile(filepath.Join(repo, "empty.txt"))
	if err != nil || again.Application == nil || again.Application.State != "partial" || string(data) != "later user file" {
		t.Fatal("partial replay changed a new user file")
	}
	if _, err := reopened.ModelWorkspace(ctx, 7, sid); !errors.Is(err, ErrSessionStateConflict) {
		t.Fatal("partial original changes admitted more model work")
	}
}
