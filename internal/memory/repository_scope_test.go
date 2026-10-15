package memory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func TestMemoryCommandsDoNotReadAnotherRepository(t *testing.T) {
	ctx := context.Background()
	ledger := openMemoryLedger(t)
	workbench := session.NewWorkbench(ledger, nil)
	module := NewLedgerMemory(ledger)
	source, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "source", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "empty target", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := module.Manage(ctx, source.ID, session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "private-repository", Content: "PRIVATE_REPOSITORY_MEMORY"})
	if err != nil {
		t.Fatal(err)
	}
	var saved Experience
	if err := json.Unmarshal([]byte(encoded), &saved); err != nil {
		t.Fatal(err)
	}
	if output, err := module.Manage(ctx, target.ID, session.MemoryCommand{Action: "read", ID: saved.ID}); err == nil || output != "" {
		t.Fatalf("unselected repository memory was readable: %s %v", output, err)
	}
	if output, err := module.Manage(ctx, target.ID, session.MemoryCommand{Action: "list"}); err != nil || strings.Contains(output, "PRIVATE_REPOSITORY_MEMORY") {
		t.Fatalf("unselected repository memory was listed: %s %v", output, err)
	}
	cli := NewLedgerCLI(module, func() session.Session { return session.Session{ID: target.ID} }, nil)
	if items := cli.LoadRelevant("PRIVATE_REPOSITORY_MEMORY"); len(items) != 0 {
		t.Fatalf("CLI loaded another repository's memory: %+v", items)
	}
	if items := cli.List(); len(items) != 0 {
		t.Fatalf("CLI listed another repository's memory: %+v", items)
	}
	targetEvents, err := ledger.Events(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Revision++
	saved.Origins[0].SessionID = target.ID
	saved.Origins[0].SourceChecksum = fmt.Sprintf("%x", sha256.Sum256(targetEvents[0].Payload))
	saved.Origins[0].Sources = []TrajectorySource{{Seq: 0, EventID: targetEvents[0].EventID, Checksum: targetEvents[0].Checksum, Type: targetEvents[0].Type}}
	catalog, err := ledger.Events(ctx, "memory-user-7")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, "memory-user-7", int64(len(catalog)), "memory/catalog-updated", map[string]any{"schema_version": 1, "owner_id": 7, "entries": []Experience{saved}}); err != nil {
		t.Fatal(err)
	}
	if output, err := module.Manage(ctx, target.ID, session.MemoryCommand{Action: "list"}); err == nil && strings.Contains(output, "PRIVATE_REPOSITORY_MEMORY") {
		t.Fatalf("memory with inconsistent repository provenance was injected: %s", output)
	}
}

func TestLegacyWorkingDirectoryChangeDoesNotRebindMemory(t *testing.T) {
	ctx := context.Background()
	store := session.NewSQLiteEventStore(filepath.Join(t.TempDir(), "ledger.sqlite"))
	defer store.Close()
	manager := session.NewManager(store)
	original, other := t.TempDir(), t.TempDir()
	created := manager.NewSession(original)
	actor, err := identity.Default().BindSession(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetActor(actor)
	manager.Append(session.RoleUser, "LEGACY_REPOSITORY_ANCHOR")
	ledger, err := store.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	module := NewLedgerMemory(ledger)
	if err := module.CommitCLIOutcome(ctx, created.ID, "Completed LEGACY_REPOSITORY_ANCHOR"); err != nil {
		t.Fatal(err)
	}
	if _, err := module.Manage(ctx, created.ID, session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "initial-repository", Content: "LEGACY_REPOSITORY_ANCHOR"}); err != nil {
		t.Fatal(err)
	}
	manager.SetWorkingDir(other)
	owner, err := OwnerForActor(actor)
	if err != nil {
		t.Fatal(err)
	}
	workbench := session.NewWorkbench(ledger, nil)
	for _, target := range []struct {
		directory string
		want      bool
	}{{other, false}, {original, true}} {
		view, err := workbench.CreateWithWorkingDir(ctx, owner, "repo", "empty target", "", target.directory)
		if err != nil {
			t.Fatal(err)
		}
		output, err := module.RecallWithOptions(ctx, owner, session.MemoryQuery{Query: "LEGACY_REPOSITORY_ANCHOR", Kind: "patterns", TargetSessionID: view.ID})
		if err != nil || strings.Contains(output, "LEGACY_REPOSITORY_ANCHOR") != target.want {
			t.Fatalf("mutable cwd changed source repository identity: %s %v", output, err)
		}
	}
}

func TestWindowsRepositoryCaseAliasKeepsMemoryIdentity(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows repository case aliases")
	}
	ctx := context.Background()
	ledger := openMemoryLedger(t)
	workbench := session.NewWorkbench(ledger, nil)
	directory := filepath.Join(t.TempDir(), "MemoryScope")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	source, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "source", "", directory)
	if err != nil {
		t.Fatal(err)
	}
	alias, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "alias", "", strings.ToUpper(directory))
	if err != nil {
		t.Fatal(err)
	}
	module := NewLedgerMemory(ledger)
	output, err := module.Manage(ctx, source.ID, session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "case-alias", Content: "Repository case alias memory"})
	var saved Experience
	if err != nil || json.Unmarshal([]byte(output), &saved) != nil {
		t.Fatalf("remember: %s %v", output, err)
	}
	output, err = module.Manage(ctx, alias.ID, session.MemoryCommand{Action: "list"})
	var recalled []Memory
	if err != nil || json.Unmarshal([]byte(output), &recalled) != nil || len(recalled) != 1 || recalled[0].ID != saved.ID {
		t.Fatalf("same Windows repository received another identity: %s %v", output, err)
	}
	if _, err := module.Manage(ctx, alias.ID, session.MemoryCommand{Action: "forget", ID: saved.ID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if output, err := module.Manage(ctx, source.ID, session.MemoryCommand{Action: "list"}); err != nil || output != "[]" {
		t.Fatalf("case alias bypassed forget: %s %v", output, err)
	}
}

func TestLegacyRepositoryMemoryKeepsIdentityRevisionAndForget(t *testing.T) {
	ctx := context.Background()
	ledger := openMemoryLedger(t)
	workbench := session.NewWorkbench(ledger, nil)
	directory := t.TempDir()
	source, err := workbench.CreateWithWorkingDir(ctx, 7, "repo", "legacy source", "", directory)
	if err != nil {
		t.Fatal(err)
	}
	events, err := ledger.Events(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy := Experience{ID: "experience-f8b63b577b8d2f07eb67b684cd8d457f455d178f82fd60b95e9079790838fcc6", Kind: "patterns", Key: "private-repository", Abstract: "Legacy source verification", Overview: "Legacy source verification", Content: "Legacy source verification", Revision: 1, UpdatedAt: time.Now().UTC(),
		Origins: []memoryOrigin{{SessionID: source.ID, SourceChecksum: fmt.Sprintf("%x", sha256.Sum256(events[0].Payload)), Manual: true, Sources: []TrajectorySource{{Seq: 0, EventID: events[0].EventID, Checksum: events[0].Checksum, Type: events[0].Type}}}}}
	if _, err := ledger.Append(ctx, "memory-user-7", 0, "memory/catalog-created", map[string]any{"schema_version": 1, "owner_id": 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(ctx, "memory-user-7", 1, "memory/catalog-updated", map[string]any{"schema_version": 1, "owner_id": 7, "entries": []Experience{legacy}}); err != nil {
		t.Fatal(err)
	}
	module := NewLedgerMemory(ledger, func(_ context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
		return &pb.MemoryReflectionResponse{SourceChecksum: request.SourceChecksum, Candidates: []*pb.MemoryCandidate{{Kind: "patterns", Key: legacy.Key, Abstract: "New source verification", Overview: "New source verification", Content: "New source verification", SourceEventIds: []string{request.Sources[0].EventId}}}}, nil
	})
	if _, err := module.Manage(ctx, source.ID, session.MemoryCommand{Action: "forget", ID: legacy.ID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	reopened := runMemoryFixtureInDirectory(t, ledger, 7, "source verification after forget", module, &memoryConversationFixture{}, directory)
	if output, err := module.Manage(ctx, reopened, session.MemoryCommand{Action: "list"}); err != nil || output != "[]" {
		t.Fatalf("automatic reflection resurrected legacy tombstone: %s %v", output, err)
	}
	output, err := module.Manage(ctx, reopened, session.MemoryCommand{Action: "remember", Kind: legacy.Kind, Key: legacy.Key, Content: "Explicitly remembered source verification", ExpectedRevision: 2})
	var restored Experience
	if err != nil || json.Unmarshal([]byte(output), &restored) != nil || restored.ID != legacy.ID || restored.Revision != 3 || restored.Deleted {
		t.Fatalf("legacy identity or revision reset: %s %v", output, err)
	}
	if err := ledger.Verify(ctx, "memory-user-7"); err != nil {
		t.Fatal(err)
	}
}

func TestReflectionKeysRemainIndependentAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	ledger := openMemoryLedger(t)
	var calls atomic.Int32
	module := NewLedgerMemory(ledger, reflectionFixture(&calls, false))
	first := runMemoryFixtureInDirectory(t, ledger, 7, "source-verified ledger testing", module, &memoryConversationFixture{}, t.TempDir())
	list := func(id string) []Memory {
		t.Helper()
		encoded, err := module.Manage(ctx, id, session.MemoryCommand{Action: "list"})
		var entries []Memory
		if err != nil || json.Unmarshal([]byte(encoded), &entries) != nil || len(entries) != 1 {
			t.Fatalf("repository reflection memory disappeared: %s %v", encoded, err)
		}
		return entries
	}
	original := list(first)[0]
	second := runMemoryFixtureInDirectory(t, ledger, 7, "source-verified ledger testing", module, &memoryConversationFixture{}, t.TempDir())
	if retained, other := list(first)[0], list(second)[0]; retained.ID != original.ID || retained.ID == other.ID {
		t.Fatalf("the same semantic key overwrote another repository: %s %s %s", original.ID, retained.ID, other.ID)
	}
	encoded, err := module.Manage(ctx, first, session.MemoryCommand{Action: "read", ID: original.ID})
	var entry Experience
	if err != nil || json.Unmarshal([]byte(encoded), &entry) != nil || entry.Revision != 1 || len(entry.Origins) != 1 || entry.Origins[0].SessionID != first {
		t.Fatalf("repository reflection provenance was merged: %s %v", encoded, err)
	}
}

func TestMemoryRejectsTamperedOrigin(t *testing.T) {
	for _, manual := range []bool{true, false} {
		t.Run(map[bool]string{true: "manual", false: "reflection"}[manual], func(t *testing.T) {
			for _, field := range []string{"checksum", "sequence", "type"} {
				t.Run(field, func(t *testing.T) {
					ctx := context.Background()
					ledger := openMemoryLedger(t)
					var calls atomic.Int32
					module := NewLedgerMemory(ledger, reflectionFixture(&calls, false))
					id := runMemoryFixture(t, ledger, 7, "source verified ledger testing", module, &memoryConversationFixture{})
					if manual {
						if _, err := module.Manage(ctx, id, session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "checksum-source", Content: "Source verified ledger testing"}); err != nil {
							t.Fatal(err)
						}
					}
					output, err := module.Manage(ctx, id, session.MemoryCommand{Action: "list"})
					var items []Memory
					if err != nil || json.Unmarshal([]byte(output), &items) != nil {
						t.Fatalf("memory list: %s %v", output, err)
					}
					var selected Memory
					for _, item := range items {
						if manual && item.Kind == "patterns" || !manual && item.Kind == "preferences" {
							selected = item
						}
					}
					output, err = module.Manage(ctx, id, session.MemoryCommand{Action: "read", ID: selected.ID})
					var entry Experience
					if err != nil || json.Unmarshal([]byte(output), &entry) != nil {
						t.Fatalf("memory read: %s %v", output, err)
					}
					entry.Revision++
					origin := &entry.Origins[len(entry.Origins)-1]
					switch field {
					case "checksum":
						origin.SourceChecksum = strings.Repeat("f", 64)
					case "sequence":
						origin.Sources[0].Seq++
					case "type":
						origin.Sources[0].Type = "forged/type"
					}
					events, err := ledger.Events(ctx, "memory-user-7")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := ledger.Append(ctx, "memory-user-7", int64(len(events)), "memory/catalog-updated", map[string]any{"schema_version": 1, "owner_id": 7, "entries": []Experience{entry}}); err != nil {
						t.Fatal(err)
					}
					if output, err := module.RecallWithOptions(ctx, 7, session.MemoryQuery{Query: "ledger testing", Kind: entry.Kind, TargetSessionID: id}); err == nil || output != "" {
						t.Fatalf("forged source %s was recalled: %s %v", field, output, err)
					}
				})
			}
		})
	}
}
