package memory

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func TestFrozenMemoryWritesCannotExposeOrChangePriorTrials(t *testing.T) {
	for _, readEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "read-off", true: "read-on"}[readEnabled], func(t *testing.T) {
			ctx := context.Background()
			workspace := t.TempDir()
			for _, args := range [][]string{{"init", "--quiet"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "--quiet", "-m", "initial"}} {
				if output, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("git: %v %s", err, output)
				}
			}
			ledger := openMemoryLedger(t)
			workbench := session.NewWorkbench(ledger, nil)
			prior, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "prior", "prior", workspace)
			if err != nil {
				t.Fatal(err)
			}
			live := NewLedgerMemory(ledger)
			encoded, err := live.Manage(ctx, prior.ID, session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "prior-note", Content: "UNSELECTED_PRIOR_CONTENT"})
			if err != nil {
				t.Fatal(err)
			}
			var priorEntry Experience
			if err := json.Unmarshal([]byte(encoded), &priorEntry); err != nil {
				t.Fatal(err)
			}
			target, err := workbench.CreateWithWorkingDir(ctx, 7, "fixture", "target", "target", workspace)
			if err != nil {
				t.Fatal(err)
			}
			policy := session.EvaluationPolicy{ContextStrategy: "metadata_on_demand", MemoryRead: readEnabled, ExecutionStrategy: "single", MaxToolRounds: 4, ContextMaxBytes: 12000}
			if err := session.BindEvaluation(ctx, ledger, target.ID, session.EvaluationBinding{SchemaVersion: 1, RunID: "target", TaskID: "target", Workspace: workspace, Model: "fixture", Policy: policy}); err != nil {
				t.Fatal(err)
			}
			runner := session.NewSessionRunner(workbench, &memoryConversationFixture{}, nil, session.SessionRunnerOptions{})
			defer runner.Close()
			if _, err := runner.SubmitMessage(ctx, session.SubmitMessageCommand{RequestID: "fixture", SessionID: target.ID, OwnerID: 7, ExpectedSeq: 2, Content: "target", Actor: identity.Default()}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				view, err := workbench.Get(ctx, 7, target.ID)
				if err != nil {
					t.Fatal(err)
				}
				if view.Run != nil && view.Run.Status == session.RunCompleted {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("fixture did not complete")
				}
				time.Sleep(10 * time.Millisecond)
			}
			frozen, err := NewFrozenView(live, nil, workspace, 7)
			if err != nil {
				t.Fatal(err)
			}
			before, err := session.ReadVerifiedSnapshot(ctx, ledger, catalogID(7))
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"retain", "forget", "remember"} {
				encoded, err := frozen.Manage(ctx, target.ID, session.MemoryCommand{Action: action, ID: priorEntry.ID, ExpectedRevision: priorEntry.Revision, Kind: "patterns", Key: "prior-note", Content: "replacement"})
				if err == nil || encoded != "" {
					t.Fatalf("%s exposed or modified an unselected prior trial", action)
				}
			}
			after, err := session.ReadVerifiedSnapshot(ctx, ledger, catalogID(7))
			if err != nil || len(after.Events) != len(before.Events) || after.Events[len(after.Events)-1].Checksum != before.Events[len(before.Events)-1].Checksum {
				t.Fatal("rejected historical writes changed the catalog")
			}
			encoded, err = frozen.Manage(ctx, target.ID, session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "current-note", Content: "current trial"})
			if err != nil {
				t.Fatal(err)
			}
			var current Experience
			if err := json.Unmarshal([]byte(encoded), &current); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"retain", "forget"} {
				encoded, err = frozen.Manage(ctx, target.ID, session.MemoryCommand{Action: action, ID: current.ID, ExpectedRevision: current.Revision})
				if err != nil || json.Unmarshal([]byte(encoded), &current) != nil || current.Content != "current trial" {
					t.Fatalf("current trial %s failed: %v", action, err)
				}
			}
			if !current.Deleted {
				t.Fatal("current trial forget was not persisted")
			}
		})
	}
}
