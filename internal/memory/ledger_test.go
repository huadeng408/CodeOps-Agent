package memory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/session"
)

type memoryConversationFixture struct {
	toolArgs string
	result   orchestrator.ToolResult
}

func (f *memoryConversationFixture) RunConversation(ctx context.Context, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error) {
	if f.toolArgs != "" {
		f.result = handlers.Tool(ctx, orchestrator.ToolCall{ID: "recall", Name: "RecallMemory", ParametersJSON: f.toolArgs})
	}
	return orchestrator.ConversationResult{Success: true, Message: "Completed: " + request.Input}, nil
}

func openMemoryLedger(t *testing.T) *session.SQLiteEventLog {
	t.Helper()
	ledger, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	return ledger
}

func runMemoryFixture(t *testing.T, ledger session.EventLog, owner uint, input string, module session.SessionMemory, fixture *memoryConversationFixture, permissions ...*permission.Controller) string {
	t.Helper()
	ctx := context.Background()
	workbench := session.NewWorkbench(ledger, nil)
	view, err := workbench.Create(ctx, owner, "repo", "memory task", input)
	if err != nil {
		t.Fatal(err)
	}
	options := session.SessionRunnerOptions{Memory: module}
	if len(permissions) > 0 {
		options.Permissions = permissions[0]
	}
	runner := session.NewSessionRunner(workbench, fixture, nil, options)
	t.Cleanup(func() { _ = runner.Close() })
	_, err = runner.SubmitMessage(ctx, session.SubmitMessageCommand{
		RequestID: "request", SessionID: view.ID, OwnerID: owner, ExpectedSeq: 1, Content: input, Actor: identity.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err := workbench.Get(ctx, owner, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Run != nil && current.Run.Status == session.RunCompleted {
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			return view.ID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture run did not reach completed terminal")
	return ""
}

func decodeMemoryRecall(t *testing.T, module *LedgerMemory, owner uint, query string, budget int) RecallResult {
	t.Helper()
	text, err := module.Recall(context.Background(), owner, query, budget)
	if err != nil {
		t.Fatal(err)
	}
	var result RecallResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLedgerMemoryAutomaticCommitScopeBudgetAndIdempotency(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := NewLedgerMemory(ledger)
	sessionID := runMemoryFixture(t, ledger, 7, "durable memory anchor", module, &memoryConversationFixture{})
	runMemoryFixture(t, ledger, 8, "durable foreign private anchor", module, &memoryConversationFixture{})
	first := decodeMemoryRecall(t, module, 7, "durable", 1200)
	if len(first.Entries) != 1 || first.Entries[0].Memory.SessionID != sessionID || first.Entries[0].Memory.SourceChecksum == "" || first.Entries[0].Memory.Checksum == "" {
		t.Fatalf("scoped recall = %+v", first)
	}
	if tiny := decodeMemoryRecall(t, module, 7, "durable", 1); tiny.Stats.Returned != 0 || tiny.Stats.Dropped != 1 || tiny.Stats.UsedTokens > 1 {
		t.Fatalf("budget was not enforced: %+v", tiny)
	}
	before, _ := ledger.Events(context.Background(), sessionID)
	if err := module.Commit(context.Background(), sessionID); err != nil {
		t.Fatal(err)
	}
	after, _ := ledger.Events(context.Background(), sessionID)
	if len(before) != len(after) {
		t.Fatal("duplicate memory commit appended another fact")
	}
	if _, err := module.Recall(context.Background(), 0, "durable", 1200); !errors.Is(err, session.ErrSessionOwnerRequired) {
		t.Fatalf("ownerless recall was not refused: %v", err)
	}
}

func TestLedgerMemoryRecoveryCommitsMissedTerminalWithoutRerunningModel(t *testing.T) {
	ledger := openMemoryLedger(t)
	sessionID := runMemoryFixture(t, ledger, 7, "restart memory anchor", nil, &memoryConversationFixture{})
	module := NewLedgerMemory(ledger)
	runner := session.NewSessionRunner(session.NewWorkbench(ledger, nil), nil, nil, session.SessionRunnerOptions{Memory: module})
	defer runner.Close()
	if err := runner.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result := decodeMemoryRecall(t, module, 7, "restart", 1200); len(result.Entries) != 1 || result.Entries[0].Memory.SessionID != sessionID {
		t.Fatalf("missed commit was not recovered: %+v", result)
	}
	before, _ := ledger.Events(context.Background(), sessionID)
	if err := runner.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := ledger.Events(context.Background(), sessionID)
	if len(before) != len(after) {
		t.Fatal("recovery was not idempotent")
	}
}

func TestLedgerMemoryRecallToolKeepsHarnessCallResultAndRejectsOwnerInjection(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := NewLedgerMemory(ledger)
	runMemoryFixture(t, ledger, 7, "recall source anchor", module, &memoryConversationFixture{})
	fixture := &memoryConversationFixture{toolArgs: `{"query":"source","max_tokens":1200}`}
	sessionID := runMemoryFixture(t, ledger, 7, "recall historical source", module, fixture)
	if fixture.result.ExitCode != 0 || !strings.Contains(fixture.result.Output, "recall source anchor") {
		t.Fatalf("RecallMemory = %+v", fixture.result)
	}
	events, _ := ledger.Events(context.Background(), sessionID)
	calls, results := 0, 0
	for _, event := range events {
		if event.Type == "tool/call" {
			calls++
		}
		if event.Type == "tool/result" {
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("memory bypassed tool audit: calls=%d results=%d", calls, results)
	}
	fixture = &memoryConversationFixture{toolArgs: `{"query":"source","owner_id":8}`}
	runMemoryFixture(t, ledger, 7, "reject owner injection", module, fixture)
	if fixture.result.ExitCode == 0 || fixture.result.Output != "" || fixture.result.Error != "invalid memory recall parameters" {
		t.Fatalf("owner injection accepted: %+v", fixture.result)
	}
}

type memoryRecallSpy struct {
	session.SessionMemory
	calls int
}

func (m *memoryRecallSpy) Recall(ctx context.Context, owner uint, query string, budget int) (string, error) {
	m.calls++
	return m.SessionMemory.Recall(ctx, owner, query, budget)
}

func TestLedgerMemoryDeniedToolNeverReadsHistoricalMemory(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := &memoryRecallSpy{SessionMemory: NewLedgerMemory(ledger)}
	fixture := &memoryConversationFixture{toolArgs: `{"query":"private anchor"}`}
	permissions := permission.NewControllerWithRules(nil, nil, []permission.AllowRule{{Tool: "RecallMemory"}})
	if permissions.Level("RecallMemory") != permission.AskSession {
		t.Fatal("memory recall must require session approval by default")
	}
	sessionID := runMemoryFixture(t, ledger, 7, "deny historical recall", module, fixture, permissions)
	if module.calls != 0 || fixture.result.Output != "" || fixture.result.Error != "tool approval denied" || fixture.result.ExitCode == 0 {
		t.Fatalf("denied recall accessed memory: calls=%d result=%+v", module.calls, fixture.result)
	}
	events, err := ledger.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	calls, results := 0, 0
	for _, event := range events {
		switch event.Type {
		case "tool/call":
			calls++
		case "tool/result":
			results++
		case "tool/dispatched":
			t.Fatal("denied recall was dispatched")
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("denied recall lost its audit pair: calls=%d results=%d", calls, results)
	}
}

func TestLedgerMemoryRewindDeletesButPendingTurnRetainsCompletedCommit(t *testing.T) {
	for _, mutation := range []string{"rewind", "pending", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			ledger := openMemoryLedger(t)
			module := NewLedgerMemory(ledger)
			sessionID := runMemoryFixture(t, ledger, 7, "obsolete memory anchor", module, &memoryConversationFixture{})
			ctx := context.Background()
			events, _ := ledger.Events(ctx, sessionID)
			switch mutation {
			case "rewind":
				if _, err := ledger.Rewind(ctx, sessionID, 1); err != nil {
					t.Fatal(err)
				}
			case "pending":
				if _, err := session.NewWorkbench(ledger, nil).AppendUserMessage(ctx, 7, sessionID, int64(len(events)), "not completed"); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := session.NewWorkbench(ledger, nil).Delete(ctx, 7, sessionID, int64(len(events))); err != nil {
					t.Fatal(err)
				}
			}
			expected := 0
			if mutation == "pending" {
				expected = 1
			}
			if result := decodeMemoryRecall(t, module, 7, "obsolete", 1200); len(result.Entries) != expected {
				t.Fatalf("stale commit resurfaced: %+v", result)
			}
			if err := module.Commit(ctx, sessionID); err == nil {
				t.Fatal("unfinished/deleted branch promoted")
			}
		})
	}
}

func TestLedgerMemoryMalformedOwnedCommitFailsClosed(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := NewLedgerMemory(ledger)
	sessionID := runMemoryFixture(t, ledger, 7, "verified source", module, &memoryConversationFixture{})
	events, _ := ledger.Events(context.Background(), sessionID)
	if _, err := ledger.Append(context.Background(), sessionID, int64(len(events)), trajectoryCommittedEvent, map[string]any{"schema_version": 1, "owner_id": 7}); err != nil {
		t.Fatal(err)
	}
	if text, err := module.Recall(context.Background(), 7, "source", 1200); !errors.Is(err, ErrTrajectoryIntegrity) || text != "" {
		t.Fatalf("invalid commit accepted: %q, %v", text, err)
	}
}

func TestMalformedTrajectorySourceSequenceFailsClosed(t *testing.T) {
	events := []session.Event{{Seq: 0, EventID: "event-0", Checksum: "checksum-0", Type: "user/message"}}
	_, err := resolveTrajectorySource(events, TrajectorySource{
		Seq: 99, EventID: "event-99", Checksum: "checksum-99", Type: "user/message",
	})
	if !errors.Is(err, ErrTrajectoryIntegrity) {
		t.Fatalf("out-of-range source was not rejected: %v", err)
	}
	_, err = resolveTrajectorySource(events, TrajectorySource{
		Seq: 0, EventID: "different-event", Checksum: "checksum-0", Type: "user/message",
	})
	if !errors.Is(err, ErrTrajectoryIntegrity) {
		t.Fatalf("mismatched source identity was not rejected: %v", err)
	}
}

type unavailableMemoryFixture struct{}

func (unavailableMemoryFixture) Commit(context.Context, string) error {
	return errors.New("fixture raw error must never be persisted")
}

func (unavailableMemoryFixture) Recall(context.Context, uint, string, int) (string, error) {
	return "", errors.New("fixture memory unavailable")
}

func TestLedgerMemoryFailureDoesNotOverwriteSuccessfulTaskTerminal(t *testing.T) {
	ledger := openMemoryLedger(t)
	sessionID := runMemoryFixture(t, ledger, 7, "task succeeded", unavailableMemoryFixture{}, &memoryConversationFixture{})
	events, err := ledger.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	completed, blocked := 0, 0
	for _, event := range events {
		if strings.Contains(string(event.Payload), "fixture raw error") {
			t.Fatal("raw memory error leaked")
		}
		if event.Type == "session/run-completed" {
			completed++
		}
		if event.Type == "memory/commit-blocked" {
			blocked++
		}
		if event.Type == "session/run-failed" || event.Type == trajectoryCommittedEvent {
			t.Fatal("memory failure changed completion or fabricated a commit")
		}
	}
	if completed != 1 || blocked != 1 {
		t.Fatalf("completed=%d blocked=%d", completed, blocked)
	}
}

func TestLedgerMemoryIgnoresForeignMalformedCommit(t *testing.T) {
	ledger := openMemoryLedger(t)
	module := NewLedgerMemory(ledger)
	runMemoryFixture(t, ledger, 7, "owned source", module, &memoryConversationFixture{})
	foreignID := runMemoryFixture(t, ledger, 8, "foreign source", module, &memoryConversationFixture{})
	events, _ := ledger.Events(context.Background(), foreignID)
	if _, err := ledger.Append(context.Background(), foreignID, int64(len(events)), trajectoryCommittedEvent, map[string]any{"invalid": true}); err != nil {
		t.Fatal(err)
	}
	if result := decodeMemoryRecall(t, module, 7, "source", 1200); len(result.Entries) != 1 {
		t.Fatalf("foreign corruption affected owned recall: %+v", result)
	}
}
