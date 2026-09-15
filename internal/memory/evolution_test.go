package memory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

func reflectionFixture(calls *atomic.Int32, invalid bool) MemoryReflector {
	return func(_ context.Context, request *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
		calls.Add(1)
		refs := []string{request.Sources[0].EventId}
		if invalid {
			refs = []string{"foreign-event"}
		}
		return &pb.MemoryReflectionResponse{SourceChecksum: request.SourceChecksum, Candidates: []*pb.MemoryCandidate{{
			Kind: "preferences", Key: "testing/ledger", Abstract: "Prefer source-verified ledger testing", Overview: "Prefer source-verified ledger testing with explicit failures.",
			Content: "Prefer source-verified ledger testing.\n" + strings.Repeat("Keep failures and provenance. ", 25), SourceEventIds: refs,
		}}}, nil
	}
}

func TestMemoryManualRetentionTagsAndSourceInvalidation(t *testing.T) {
	ctx := context.Background()
	ledger := openMemoryLedger(t)
	id := runMemoryFixture(t, ledger, 7, "retention fixture", nil, &memoryConversationFixture{})
	module := NewLedgerMemory(ledger)
	_, empty, _, err := module.catalog(ctx, 7)
	if err != nil || len(empty) != 0 {
		t.Fatalf("first catalog is not empty: %v %v", empty, err)
	}
	command := session.MemoryCommand{Action: "remember", Kind: "patterns", Key: "ledger/retention", Content: "Keep retention source checks explicit", Tags: []string{"testing"}, TTLSeconds: 60}
	encoded, err := module.Manage(ctx, id, command)
	if err != nil {
		t.Fatal(err)
	}
	var entry Experience
	if json.Unmarshal([]byte(encoded), &entry) != nil || entry.ExpiresAt == nil || len(entry.Tags) != 1 {
		t.Fatalf("retained memory = %s", encoded)
	}
	items, err := module.experienceItems(ctx, 7, "full")
	if err != nil || len(items) != 1 || !strings.Contains(strings.Join(items[0].Tags, ","), "testing") {
		t.Fatalf("memory tags lost: %v %v", items, err)
	}
	err = module.updateCatalog(ctx, 7, "", func(current map[string]Experience) ([]Experience, error) {
		item := current[entry.ID]
		item.Revision++
		expired := time.Now().Add(-time.Second)
		item.ExpiresAt = &expired
		return []Experience{item}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err = module.experienceItems(ctx, 7, "full")
	if err != nil || len(items) != 0 {
		t.Fatal("expired memory recalled")
	}
	if _, err := module.Manage(ctx, id, session.MemoryCommand{Action: "retain", ID: entry.ID, ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := module.Manage(ctx, id, session.MemoryCommand{Action: "forget", ID: entry.ID, ExpectedRevision: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := module.Manage(ctx, id, session.MemoryCommand{Action: "retain", ID: entry.ID, ExpectedRevision: 4}); err == nil {
		t.Fatal("retain resurrected forgotten memory")
	}
	command.ExpectedRevision = 4
	if _, err := module.Manage(ctx, id, command); err != nil {
		t.Fatal(err)
	}
	workbench := session.NewWorkbench(ledger, nil)
	view, _ := workbench.Get(ctx, 7, id)
	if err := workbench.Delete(ctx, 7, id, int64(view.EventCount)); err != nil {
		t.Fatal(err)
	}
	items, err = module.experienceItems(ctx, 7, "full")
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted source recalled: %v %v", items, err)
	}
}

func TestMemoryConcurrentReflectionPromotesOneDurableProposal(t *testing.T) {
	ctx := context.Background()
	ledger := openMemoryLedger(t)
	id := runMemoryFixture(t, ledger, 7, "concurrent ledger reflection", nil, &memoryConversationFixture{})
	var calls atomic.Int32
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	model := reflectionFixture(&calls, false)
	module := NewLedgerMemory(ledger, func(ctx context.Context, req *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error) {
		arrived <- struct{}{}
		<-release
		return model(ctx, req)
	})
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errorsCh <- module.Commit(ctx, id) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(3 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("concurrent reflection did not reach model")
		}
	}
	close(release)
	wg.Wait()
	for i := 0; i < 2; i++ {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	events, _ := ledger.Events(ctx, id)
	proposals := 0
	for _, event := range events {
		if event.Type == "memory/reflection-proposed" {
			proposals++
		}
	}
	_, entries, _, err := module.catalog(ctx, 7)
	if err != nil || len(entries) != 1 || entries[experienceID("preferences", "testing/ledger")].Revision != 1 || proposals != 1 {
		t.Fatalf("non-idempotent promotion: entries=%v proposals=%d err=%v", entries, proposals, err)
	}
}

func TestMemoryReflectionEvolutionProgressiveRecallAndForget(t *testing.T) {
	ledger := openMemoryLedger(t)
	var calls atomic.Int32
	module := NewLedgerMemory(ledger, reflectionFixture(&calls, false))
	first := runMemoryFixture(t, ledger, 7, "source-verified ledger testing", module, &memoryConversationFixture{})
	second := runMemoryFixture(t, ledger, 7, "source-verified ledger testing again", module, &memoryConversationFixture{})
	_, entries, _, err := module.catalog(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	id := experienceID("preferences", "testing/ledger")
	if len(entries) != 1 || entries[id].Revision != 2 || len(entries[id].Origins) != 2 {
		t.Fatalf("evolution = %+v", entries)
	}
	for _, detail := range []string{"abstract", "overview", "full"} {
		encoded, err := module.RecallWithOptions(context.Background(), 7, session.MemoryQuery{Query: "ledger", Kind: "preferences", Detail: detail, MaxTokens: 1200})
		if err != nil {
			t.Fatal(err)
		}
		var recalled RecallResult
		if json.Unmarshal([]byte(encoded), &recalled) != nil || len(recalled.Entries) != 1 || recalled.Entries[0].Memory.Detail != detail {
			t.Fatalf("recall %s = %s", detail, encoded)
		}
		if detail == "full" && len(recalled.Entries[0].Memory.Content) < 480 {
			t.Fatal("full detail was truncated to an overview")
		}
	}
	if err := module.Commit(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("recovery reran an already-applied reflection")
	}
	if _, err := module.Manage(context.Background(), second, session.MemoryCommand{Action: "forget", ID: id, ExpectedRevision: 1}); !errors.Is(err, session.ErrSequenceConflict) {
		t.Fatalf("stale CAS = %v", err)
	}
	if _, err := module.Manage(context.Background(), second, session.MemoryCommand{Action: "forget", ID: id, ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	runMemoryFixture(t, ledger, 7, "source-verified ledger testing after forget", module, &memoryConversationFixture{})
	items, err := module.experienceItems(context.Background(), 7, "full")
	if err != nil || len(items) != 0 {
		t.Fatalf("forgotten memory resurrected: %+v %v", items, err)
	}
	foreign, err := module.experienceItems(context.Background(), 8, "full")
	if err != nil || len(foreign) != 0 {
		t.Fatal("cross-owner recall")
	}
}

func TestMemoryReflectionInvalidProvenanceKeepsSuccessfulTask(t *testing.T) {
	ledger := openMemoryLedger(t)
	var calls atomic.Int32
	module := NewLedgerMemory(ledger, reflectionFixture(&calls, true))
	id := runMemoryFixture(t, ledger, 7, "ledger testing", module, &memoryConversationFixture{})
	events, _ := ledger.Events(context.Background(), id)
	blocked, terminal := false, false
	for _, event := range events {
		blocked = blocked || event.Type == "memory/commit-blocked"
		terminal = terminal || event.Type == "session/run-completed"
	}
	items, err := module.experienceItems(context.Background(), 7, "full")
	if !blocked || !terminal || err != nil || len(items) != 0 {
		t.Fatalf("unsafe proposal changed task outcome: blocked=%v terminal=%v items=%v err=%v", blocked, terminal, items, err)
	}
}

func TestLegacyCLIMemoryUsesSameLedgerAndAuthenticatedIdentity(t *testing.T) {
	store := session.NewSQLiteEventStore(t.TempDir() + "/cli.sqlite")
	defer store.Close()
	manager := session.NewManager(store)
	state := manager.NewSession(t.TempDir())
	actor, err := identity.Default().BindSession(state.ID)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetActor(actor)
	manager.Append(session.RoleUser, "source-verified CLI ledger testing")
	ledger, err := store.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	module := NewLedgerMemory(ledger)
	if err := module.CommitCLIOutcome(context.Background(), state.ID, "Completed CLI ledger testing"); err != nil {
		t.Fatal(err)
	}
	owner, err := OwnerForActor(actor)
	if err != nil {
		t.Fatal(err)
	}
	result := decodeMemoryRecall(t, module, owner, "CLI", 1200)
	if len(result.Entries) != 1 {
		t.Fatalf("legacy recall = %+v", result)
	}
	other := actor
	other.ActorID = "another-actor"
	otherOwner, _ := OwnerForActor(other)
	if found := decodeMemoryRecall(t, module, otherOwner, "CLI", 1200); len(found.Entries) != 0 {
		t.Fatal("legacy actor memory leaked")
	}
}
