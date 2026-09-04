package codeagent_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"code-agent/internal/identity"
	"code-agent/internal/session"
	_ "modernc.org/sqlite"
)

func TestSQLiteEventLogAppendsDurableHashChainedEvents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.sqlite")
	log, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}

	first, err := log.Append(ctx, "session-1", 0, "user/message", map[string]string{"content": "hello"})
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}
	if first.Seq != 0 || first.SessionID != "session-1" || first.Type != "user/message" {
		t.Fatalf("unexpected first event: %+v", first)
	}
	if first.EventID == "" || first.Checksum == "" || first.PrevChecksum != "" {
		t.Fatalf("first event lacks chain identity: %+v", first)
	}

	second, err := log.Append(ctx, "session-1", 1, "assistant/message", map[string]string{"content": "world"})
	if err != nil {
		t.Fatalf("append second event: %v", err)
	}
	if second.Seq != 1 || second.PrevChecksum != first.Checksum {
		t.Fatalf("second event does not link to first: %+v", second)
	}
	if err := log.Verify(ctx, "session-1"); err != nil {
		t.Fatalf("verify chain: %v", err)
	}

	if err := log.Close(); err != nil {
		t.Fatalf("close event log: %v", err)
	}
	reopened, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("reopen event log: %v", err)
	}
	defer reopened.Close()
	events, err := reopened.Events(ctx, "session-1")
	if err != nil {
		t.Fatalf("read events after reopen: %v", err)
	}
	if len(events) != 2 || events[0].Checksum != first.Checksum || events[1].Checksum != second.Checksum {
		t.Fatalf("durable events changed after reopen: %+v", events)
	}
	var payload map[string]string
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || payload["content"] != "hello" {
		t.Fatalf("unexpected durable payload: %s", events[0].Payload)
	}
}

func TestSQLiteEventLogRejectsStaleSequenceWithoutWriting(t *testing.T) {
	ctx := context.Background()
	log, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer log.Close()

	if _, err := log.Append(ctx, "session-1", 0, "user/message", "one"); err != nil {
		t.Fatalf("append initial event: %v", err)
	}
	if _, err := log.Append(ctx, "session-1", 0, "user/message", "stale"); !errors.Is(err, session.ErrSequenceConflict) {
		t.Fatalf("stale append error = %v, want ErrSequenceConflict", err)
	}
	events, err := log.Events(ctx, "session-1")
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("stale append changed event count to %d", len(events))
	}
}

func TestSQLiteEventLogSerializesConcurrentExpectedSequenceWriters(t *testing.T) {
	ctx := context.Background()
	log, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer log.Close()

	if _, err := log.Append(ctx, "session-1", 0, "user/message", "seed"); err != nil {
		t.Fatalf("append seed event: %v", err)
	}
	const writers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var appended []session.Event
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			event, appendErr := log.Append(ctx, "session-1", 1, "tool/result", map[string]int{"writer": i})
			if appendErr == nil {
				mu.Lock()
				appended = append(appended, event)
				mu.Unlock()
			} else if !errors.Is(appendErr, session.ErrSequenceConflict) {
				t.Errorf("writer %d unexpected error: %v", i, appendErr)
			}
		}(i)
	}
	wg.Wait()
	if len(appended) != 1 {
		t.Fatalf("successful concurrent appends = %d, want 1", len(appended))
	}
	events, err := log.Events(ctx, "session-1")
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 2 || events[1].Seq != 1 {
		t.Fatalf("concurrent append produced invalid sequence: %+v", events)
	}
	if err := log.Verify(ctx, "session-1"); err != nil {
		t.Fatalf("verify concurrent chain: %v", err)
	}
}

func TestSQLiteEventLogRejectsTamperedPayload(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.sqlite")
	log, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	if _, err := log.Append(ctx, "session-1", 0, "user/message", map[string]string{"content": "original"}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close event log: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open tamper connection: %v", err)
	}
	if _, err := db.Exec(`UPDATE session_events SET payload = ? WHERE session_id = ? AND seq = ?`, `{"content":"tampered"}`, "session-1", 0); err != nil {
		t.Fatalf("tamper event payload: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close tamper connection: %v", err)
	}

	reopened, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("reopen event log: %v", err)
	}
	defer reopened.Close()
	if err := reopened.Verify(ctx, "session-1"); !errors.Is(err, session.ErrEventIntegrity) {
		t.Fatalf("tampered verification error = %v, want ErrEventIntegrity", err)
	}
}

func TestSQLiteEventLogDoesNotAppendToTamperedHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.sqlite")
	log, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	if _, err := log.Append(ctx, "session-1", 0, "user/message", "original"); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close event log: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open tamper connection: %v", err)
	}
	if _, err := db.Exec(`UPDATE session_events SET payload = ? WHERE session_id = ? AND seq = ?`, `"tampered"`, "session-1", 0); err != nil {
		t.Fatalf("tamper event payload: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close tamper connection: %v", err)
	}
	reopened, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("reopen event log: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.Append(ctx, "session-1", 1, "assistant/message", "must not land"); !errors.Is(err, session.ErrEventIntegrity) {
		t.Fatalf("append after tamper error = %v, want ErrEventIntegrity", err)
	}
	events, err := reopened.Events(ctx, "session-1")
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("tampered history was extended: %+v", events)
	}
}

func TestSQLiteEventLogProjectsOnlySurfaceEventsAndSupportsReplacement(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.sqlite")
	log, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}

	first, err := log.AppendSurface(ctx, "session-1", 0, "user/message", map[string]string{"content": "old user"}, session.SurfaceOperation{Op: "append"})
	if err != nil {
		t.Fatalf("append user surface event: %v", err)
	}
	if _, err := log.Append(ctx, "session-1", 1, "compaction/start", map[string]bool{"locked": true}); err != nil {
		t.Fatalf("append log-only event: %v", err)
	}
	second, err := log.AppendSurface(ctx, "session-1", 2, "assistant/message", map[string]string{"content": "old assistant"}, session.SurfaceOperation{Op: "append"})
	if err != nil {
		t.Fatalf("append assistant surface event: %v", err)
	}

	surface, err := log.Surface(ctx, "session-1")
	if err != nil {
		t.Fatalf("derive initial surface: %v", err)
	}
	if len(surface) != 2 || surface[0].Seq != first.Seq || surface[1].Seq != second.Seq {
		t.Fatalf("unexpected initial surface: %+v", surface)
	}

	replacement, err := log.AppendSurface(ctx, "session-1", 3, "user/message", map[string]string{"content": "summary"}, session.SurfaceOperation{Op: "replace", Start: first.Seq, End: second.Seq})
	if err != nil {
		t.Fatalf("append replacement event: %v", err)
	}
	surface, err = log.Surface(ctx, "session-1")
	if err != nil {
		t.Fatalf("derive replaced surface: %v", err)
	}
	if len(surface) != 1 || surface[0].Seq != replacement.Seq {
		t.Fatalf("replacement did not hide old nodes: %+v", surface)
	}
	if err := log.Verify(ctx, "session-1"); err != nil {
		t.Fatalf("verify surface chain: %v", err)
	}

	if err := log.Close(); err != nil {
		t.Fatalf("close event log: %v", err)
	}
	reopened, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("reopen event log: %v", err)
	}
	defer reopened.Close()
	replayed, err := reopened.Surface(ctx, "session-1")
	if err != nil {
		t.Fatalf("replay surface: %v", err)
	}
	if len(replayed) != 1 || replayed[0].Checksum != replacement.Checksum {
		t.Fatalf("surface replay changed projection: %+v", replayed)
	}
}

func TestSQLiteEventLogRejectsInvalidSurfaceOperation(t *testing.T) {
	ctx := context.Background()
	log, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer log.Close()

	cases := []session.SurfaceOperation{
		{Op: "replace", Start: -1, End: 0},
		{Op: "replace", Start: 2, End: 1},
		{Op: "discard", Start: 0, End: 0},
	}
	for _, operation := range cases {
		if _, err := log.AppendSurface(ctx, "session-1", 0, "user/message", "payload", operation); err == nil {
			t.Fatalf("operation %+v was accepted", operation)
		}
	}
}

func TestSQLiteEventLogImportsLegacySnapshotReadOnlyAndIdempotently(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	legacyStore := session.NewSQLiteStore(path)
	legacy := session.Session{
		ID:         "legacy-session",
		WorkingDir: filepath.Join(t.TempDir(), "workspace"),
		Messages: []session.Message{{
			Role:    session.RoleUser,
			Content: "preserve this message exactly",
		}},
	}
	if err := legacyStore.Save(ctx, legacy); err != nil {
		t.Fatalf("save legacy snapshot: %v", err)
	}
	if err := legacyStore.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	readLegacyPayload := func() string {
		db, openErr := sql.Open("sqlite", path)
		if openErr != nil {
			t.Fatalf("open legacy database: %v", openErr)
		}
		defer db.Close()
		var payload string
		if queryErr := db.QueryRow(`SELECT payload FROM sessions WHERE id = ?`, legacy.ID).Scan(&payload); queryErr != nil {
			t.Fatalf("read legacy payload: %v", queryErr)
		}
		return payload
	}
	before := readLegacyPayload()

	log, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer log.Close()
	imported, created, err := log.ImportLegacySnapshot(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("import legacy snapshot: %v", err)
	}
	if !created || imported.Type != "legacy/import" || imported.Seq != 0 {
		t.Fatalf("unexpected import event: created=%v event=%+v", created, imported)
	}
	var importPayload session.LegacyImportPayload
	if err := json.Unmarshal(imported.Payload, &importPayload); err != nil {
		t.Fatalf("decode import payload: %v", err)
	}
	if importPayload.SessionID != legacy.ID || importPayload.SourceSHA256 == "" || string(importPayload.Snapshot) != before {
		t.Fatalf("import payload did not preserve source: %+v", importPayload)
	}
	if after := readLegacyPayload(); after != before {
		t.Fatalf("legacy snapshot changed during import: before=%q after=%q", before, after)
	}

	repeated, created, err := log.ImportLegacySnapshot(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("repeat legacy import: %v", err)
	}
	if created || repeated.EventID != imported.EventID {
		t.Fatalf("repeat import was not idempotent: created=%v event=%+v", created, repeated)
	}
	events, err := log.Events(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("read imported events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("repeat import appended %d events", len(events))
	}
}

func TestSQLiteEventLogRejectsChangedLegacySnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	legacyStore := session.NewSQLiteStore(path)
	legacy := session.Session{ID: "legacy-session", WorkingDir: "workspace"}
	if err := legacyStore.Save(ctx, legacy); err != nil {
		t.Fatalf("save legacy snapshot: %v", err)
	}
	if err := legacyStore.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}
	log, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	if _, _, err := log.ImportLegacySnapshot(ctx, legacy.ID); err != nil {
		t.Fatalf("initial legacy import: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close event log: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec(`UPDATE sessions SET payload = ? WHERE id = ?`, `{"id":"legacy-session","working_dir":"changed"}`, legacy.ID); err != nil {
		t.Fatalf("mutate legacy snapshot: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	reopened, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("reopen event log: %v", err)
	}
	defer reopened.Close()
	if _, _, err := reopened.ImportLegacySnapshot(ctx, legacy.ID); !errors.Is(err, session.ErrLegacySnapshotChanged) {
		t.Fatalf("changed snapshot error = %v, want ErrLegacySnapshotChanged", err)
	}
	events, err := reopened.Events(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("read events after changed snapshot: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("changed snapshot appended an event: %+v", events)
	}
}

func TestSQLiteEventStorePersistsManagerStateOnlyInEventLog(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	store := session.NewSQLiteEventStore(path)
	manager := session.NewManager(store)
	created := manager.NewSession("workspace")
	manager.Append(session.RoleUser, "event-backed message")
	manager.SetMode("plan")
	if err := store.Close(); err != nil {
		t.Fatalf("close event-backed store: %v", err)
	}

	reopened := session.NewSQLiteEventStore(path)
	defer reopened.Close()
	reloaded := session.NewManager(reopened)
	loaded, err := reloaded.Load(ctx, created.ID)
	if err != nil {
		t.Fatalf("load event-backed manager state: %v", err)
	}
	if loaded.ID != created.ID || loaded.Mode != "plan" || len(loaded.Messages) != 1 || loaded.Messages[0].Content != "event-backed message" {
		t.Fatalf("unexpected event-backed state: %+v", loaded)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open event-backed database: %v", err)
	}
	defer db.Close()
	var sessionTableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&sessionTableCount); err != nil {
		t.Fatalf("inspect legacy sessions table: %v", err)
	}
	if sessionTableCount != 0 {
		t.Fatalf("event-backed store created mutable legacy sessions table")
	}
	var stateEvents int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_events WHERE session_id = ? AND type = 'session/state'`, created.ID).Scan(&stateEvents); err != nil {
		t.Fatalf("count state events: %v", err)
	}
	if stateEvents < 3 {
		t.Fatalf("state events = %d, want initial plus two updates", stateEvents)
	}
}

func TestSQLiteEventStoreRejectsLegacySourceChangeAfterStateAdvance(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	legacyStore := session.NewSQLiteStore(path)
	legacy := session.Session{ID: "legacy-session", WorkingDir: "workspace"}
	if err := legacyStore.Save(ctx, legacy); err != nil {
		t.Fatalf("save legacy snapshot: %v", err)
	}
	if err := legacyStore.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	store := session.NewSQLiteEventStore(path)
	manager := session.NewManager(store)
	loaded, err := manager.Load(ctx, legacy.ID)
	if err != nil || loaded.ID != legacy.ID {
		t.Fatalf("initial legacy load: session=%+v err=%v", loaded, err)
	}
	manager.Append(session.RoleUser, "new event state")
	if err := store.Close(); err != nil {
		t.Fatalf("close event store: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec(`UPDATE sessions SET payload = ? WHERE id = ?`, `{"id":"legacy-session","working_dir":"changed"}`, legacy.ID); err != nil {
		t.Fatalf("mutate legacy snapshot: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	reopened := session.NewSQLiteEventStore(path)
	defer reopened.Close()
	if _, err := reopened.Load(ctx, legacy.ID); !errors.Is(err, session.ErrLegacySnapshotChanged) {
		t.Fatalf("load after source change error = %v, want ErrLegacySnapshotChanged", err)
	}
}

func TestManagerDoesNotAdvanceMemoryWhenEventStoreSaveFails(t *testing.T) {
	store := &failingSessionStore{err: errors.New("durability unavailable")}
	manager := session.NewManager(store)
	created := manager.NewSession("workspace")
	if created.ID != "" {
		t.Fatalf("failed initial save exposed a durable session: %+v", created)
	}
	current := manager.Current()
	if current.ID != "" || len(current.Messages) != 0 {
		t.Fatalf("manager advanced memory after failed save: %+v", current)
	}
}

func TestManagerAppendDoesNotAdvanceMemoryWhenEventStoreSaveFails(t *testing.T) {
	store := &failingSessionStore{err: errors.New("durability unavailable")}
	manager := session.NewManager(store)
	current := manager.Append(session.RoleUser, "must not be visible")
	if current.ID != "" || len(current.Messages) != 0 {
		t.Fatalf("append advanced memory after failed save: %+v", current)
	}
	if got := manager.Messages(); len(got) != 0 {
		t.Fatalf("failed append left messages in memory: %+v", got)
	}
}

type failingSessionStore struct {
	err error
}

func (s *failingSessionStore) Save(context.Context, session.Session) error { return s.err }
func (s *failingSessionStore) Load(context.Context, string) (*session.Session, error) {
	return nil, session.ErrNotFound
}
func (s *failingSessionStore) List(context.Context) ([]session.Session, error) { return nil, s.err }

func TestSQLiteEventLogForkCopiesPrefixAndKeepsSessionsIsolated(t *testing.T) {
	ctx := context.Background()
	log, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer log.Close()

	_, err = log.AppendSurface(ctx, "parent", 0, "user/message", map[string]string{"content": "parent"}, session.SurfaceOperation{Op: "append"})
	if err != nil {
		t.Fatalf("append parent event: %v", err)
	}
	if _, err := log.Append(ctx, "parent", 1, "compaction/start", "log-only"); err != nil {
		t.Fatalf("append parent log event: %v", err)
	}
	if err := log.Fork(ctx, "parent", "child", 1); err != nil {
		t.Fatalf("fork session: %v", err)
	}
	parent, err := log.Events(ctx, "parent")
	if err != nil {
		t.Fatalf("read parent events: %v", err)
	}
	child, err := log.Events(ctx, "child")
	if err != nil {
		t.Fatalf("read child events: %v", err)
	}
	if len(parent) != 2 || len(child) != 2 {
		t.Fatalf("unexpected fork event counts: parent=%d child=%d", len(parent), len(child))
	}
	if child[0].SessionID != "child" || child[0].Type != parent[0].Type || string(child[0].Payload) != string(parent[0].Payload) {
		t.Fatalf("fork did not copy event content: parent=%+v child=%+v", parent[0], child[0])
	}
	if child[0].Checksum == parent[0].Checksum || child[0].PrevChecksum != "" {
		t.Fatalf("fork did not create an independent hash chain: %+v", child[0])
	}
	if _, err := log.Append(ctx, "child", 2, "assistant/message", "child-only"); err != nil {
		t.Fatalf("append child-only event: %v", err)
	}
	parent, _ = log.Events(ctx, "parent")
	if len(parent) != 2 {
		t.Fatalf("child append changed parent history: %+v", parent)
	}
	if err := log.Verify(ctx, "child"); err != nil {
		t.Fatalf("verify child chain: %v", err)
	}
}

func TestSQLiteEventLogRewindRebuildsSurfaceWithoutDeletingHistory(t *testing.T) {
	ctx := context.Background()
	log, err := session.OpenSQLiteEventLog(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer log.Close()

	if _, err := log.AppendSurface(ctx, "session-1", 0, "user/message", "first", session.SurfaceOperation{Op: "append"}); err != nil {
		t.Fatalf("append first event: %v", err)
	}
	if _, err := log.AppendSurface(ctx, "session-1", 1, "assistant/message", "second", session.SurfaceOperation{Op: "append"}); err != nil {
		t.Fatalf("append second event: %v", err)
	}
	if _, err := log.Rewind(ctx, "session-1", 0); err != nil {
		t.Fatalf("rewind session: %v", err)
	}
	if _, err := log.AppendSurface(ctx, "session-1", 3, "user/message", "after rewind", session.SurfaceOperation{Op: "append"}); err != nil {
		t.Fatalf("append after rewind: %v", err)
	}
	surface, err := log.Surface(ctx, "session-1")
	if err != nil {
		t.Fatalf("derive rewound surface: %v", err)
	}
	if len(surface) != 2 || string(surface[0].Payload) != `"first"` || string(surface[1].Payload) != `"after rewind"` {
		t.Fatalf("rewind surface = %+v", surface)
	}
	events, err := log.Events(ctx, "session-1")
	if err != nil {
		t.Fatalf("read rewind history: %v", err)
	}
	if len(events) != 4 || events[2].Type != "session/rewind" {
		t.Fatalf("rewind deleted or reordered history: %+v", events)
	}
	if err := log.Verify(ctx, "session-1"); err != nil {
		t.Fatalf("verify rewind chain: %v", err)
	}
}

func TestSQLiteEventStoreRewindReplaysEarlierRewindMarkers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	store := session.NewSQLiteEventStore(path)
	t.Cleanup(func() { _ = store.Close() })
	manager := session.NewManager(store)
	created := manager.NewSession("workspace")
	manager.Append(session.RoleUser, "first")
	manager.Append(session.RoleAssistant, "second")

	events, err := sessionEvents(ctx, path, created.ID)
	if err != nil {
		t.Fatalf("read initial session events: %v", err)
	}
	firstStateSeq := stateSeqWithMessageCount(t, events, 1)
	if _, err := store.Rewind(ctx, created.ID, firstStateSeq); err != nil {
		t.Fatalf("first rewind: %v", err)
	}
	afterFirst, err := sessionEvents(ctx, path, created.ID)
	if err != nil {
		t.Fatalf("read events after first rewind: %v", err)
	}
	firstMarkerSeq := int64(len(afterFirst) - 2)

	rewound, err := store.Rewind(ctx, created.ID, firstMarkerSeq)
	if err != nil {
		t.Fatalf("rewind to earlier marker: %v", err)
	}
	if len(rewound.Messages) != 1 || rewound.Messages[0].Content != "first" {
		t.Fatalf("rewind replay = %+v, want the first-message projection", rewound)
	}
}

func TestSQLiteEventStoreRewindRollsBackMarkerWhenStateWriteFails(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	store := session.NewSQLiteEventStore(path)
	manager := session.NewManager(store)
	created := manager.NewSession("workspace")
	manager.Append(session.RoleUser, "first")
	manager.Append(session.RoleAssistant, "second")

	if err := store.Close(); err != nil {
		t.Fatalf("close event store before failure injection: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open session database: %v", err)
	}
	_, err = db.Exec("CREATE TRIGGER fail_rewind_state BEFORE INSERT ON session_events\n" +
		"WHEN NEW.session_id = '" + created.ID + "' AND NEW.type = 'session/state'\n" +
		"BEGIN SELECT RAISE(ABORT, 'injected rewind state failure'); END;")
	if err != nil {
		_ = db.Close()
		t.Fatalf("install rewind failure trigger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close session database: %v", err)
	}

	store = session.NewSQLiteEventStore(path)
	t.Cleanup(func() { _ = store.Close() })
	loaded, err := store.Load(ctx, created.ID)
	if err != nil {
		t.Fatalf("load session for rewind failure: %v", err)
	}
	stateSeq := stateSeqWithMessageCount(t, mustSessionEvents(t, path, created.ID), 1)
	if _, err := store.Rewind(ctx, loaded.ID, stateSeq); err == nil {
		t.Fatal("rewind succeeded despite injected state failure")
	}

	events, err := sessionEvents(ctx, path, created.ID)
	if err != nil {
		t.Fatalf("read events after failed rewind: %v", err)
	}
	for _, event := range events {
		if event.Type == "session/rewind" {
			t.Fatalf("failed rewind left marker at seq %d", event.Seq)
		}
	}
}

func TestSQLiteEventLogForkRollsBackPartialTargetOnInsertFailure(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.sqlite")
	log, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer log.Close()
	for seq := int64(0); seq < 3; seq++ {
		if _, err := log.Append(ctx, "parent", seq, "user/message", map[string]int{"seq": int(seq)}); err != nil {
			t.Fatalf("append parent event %d: %v", seq, err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fork database: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_fork_insert BEFORE INSERT ON session_events
WHEN NEW.session_id = 'child' AND NEW.seq = 1
BEGIN SELECT RAISE(ABORT, 'injected fork failure'); END;`); err != nil {
		_ = db.Close()
		t.Fatalf("install fork failure trigger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fork database: %v", err)
	}

	if err := log.Fork(ctx, "parent", "child", 2); err == nil {
		t.Fatal("fork succeeded despite injected insert failure")
	}
	child, err := log.Events(ctx, "child")
	if err != nil {
		t.Fatalf("read child events after failed fork: %v", err)
	}
	if len(child) != 0 {
		t.Fatalf("failed fork left partial child history: %+v", child)
	}
}

func TestSQLiteEventStoreForkMaterializesLegacySnapshotIndependently(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	legacyStore := session.NewSQLiteStore(path)
	legacy := session.Session{
		ID:         "legacy-parent",
		WorkingDir: "workspace",
		Messages: []session.Message{{
			Role:    session.RoleUser,
			Content: "legacy message",
		}},
	}
	if err := legacyStore.Save(ctx, legacy); err != nil {
		t.Fatalf("save legacy source: %v", err)
	}
	if err := legacyStore.Close(); err != nil {
		t.Fatalf("close legacy source: %v", err)
	}

	store := session.NewSQLiteEventStore(path)
	manager := session.NewManager(store)
	loaded, err := manager.Load(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("import legacy source: %v", err)
	}
	manager.Append(session.RoleAssistant, "new message")
	events := mustSessionEvents(t, path, legacy.ID)
	targetSeq := stateSeqWithMessageCount(t, events, len(loaded.Messages)+1)
	if _, err := store.Fork(ctx, legacy.ID, "legacy-child", targetSeq); err != nil {
		t.Fatalf("fork imported legacy source: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close event store: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database for mutation: %v", err)
	}
	if _, err := db.Exec(`UPDATE sessions SET payload = ? WHERE id = ?`, `{"id":"legacy-parent","working_dir":"changed"}`, legacy.ID); err != nil {
		_ = db.Close()
		t.Fatalf("mutate source legacy snapshot: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close mutated legacy database: %v", err)
	}

	childStore := session.NewSQLiteEventStore(path)
	defer childStore.Close()
	child, err := childStore.Load(ctx, "legacy-child")
	if err != nil {
		t.Fatalf("load fork after source mutation: %v", err)
	}
	if len(child.Messages) != 2 || child.Messages[0].Content != "legacy message" || child.Messages[1].Content != "new message" {
		t.Fatalf("forked legacy state = %+v, want two preserved messages", child.Messages)
	}
}

func TestSQLiteEventStoreForkRebindsActorToChildSession(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")
	store := session.NewSQLiteEventStore(path)
	t.Cleanup(func() { _ = store.Close() })
	manager := session.NewManager(store)
	parent := manager.NewSession("workspace")
	actor, err := (identity.Actor{
		SchemaVersion: identity.SchemaVersion,
		ActorID:       "user:42",
		Subject:       "alice",
		TenantID:      "org:7",
		Roles:         []string{"USER", "REVIEWER"},
	}).BindSession(parent.ID)
	if err != nil {
		t.Fatalf("bind parent actor: %v", err)
	}
	manager.SetActor(actor)
	manager.Append(session.RoleUser, "fork with actor")

	events := mustSessionEvents(t, path, parent.ID)
	targetSeq := stateSeqWithMessageCount(t, events, 1)
	child, err := store.Fork(ctx, parent.ID, "child-with-actor", targetSeq)
	if err != nil {
		t.Fatalf("fork session with actor: %v", err)
	}
	if child.ID != "child-with-actor" {
		t.Fatalf("child id = %q, want child-with-actor", child.ID)
	}
	if child.Actor.ActorID != actor.ActorID || child.Actor.Subject != actor.Subject || child.Actor.TenantID != actor.TenantID {
		t.Fatalf("child actor identity changed: got=%+v want=%+v", child.Actor, actor)
	}
	if len(child.Actor.Roles) != len(actor.Roles) || child.Actor.Roles[0] != "USER" || child.Actor.Roles[1] != "REVIEWER" {
		t.Fatalf("child actor roles changed: got=%v want=%v", child.Actor.Roles, actor.Roles)
	}
	if child.Actor.SessionID != child.ID {
		t.Fatalf("child actor session = %q, want %q", child.Actor.SessionID, child.ID)
	}
	if err := child.Actor.Validate(); err != nil {
		t.Fatalf("forked child actor is invalid: %v", err)
	}
	rewound, err := store.Rewind(ctx, child.ID, targetSeq)
	if err != nil {
		t.Fatalf("rewind child session with actor: %v", err)
	}
	if rewound.Actor.SessionID != child.ID {
		t.Fatalf("rewound child actor session = %q, want %q", rewound.Actor.SessionID, child.ID)
	}
	loadedParent, err := store.Load(ctx, parent.ID)
	if err != nil {
		t.Fatalf("reload parent after fork: %v", err)
	}
	if loadedParent.Actor.SessionID != parent.ID {
		t.Fatalf("parent actor was rebound: got session %q, want %q", loadedParent.Actor.SessionID, parent.ID)
	}
}

func sessionEvents(ctx context.Context, path, sessionID string) ([]session.Event, error) {
	log, err := session.OpenSQLiteEventLog(path)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	return log.Events(ctx, sessionID)
}

func mustSessionEvents(t *testing.T, path, sessionID string) []session.Event {
	t.Helper()
	events, err := sessionEvents(context.Background(), path, sessionID)
	if err != nil {
		t.Fatalf("read session events: %v", err)
	}
	return events
}

func stateSeqWithMessageCount(t *testing.T, events []session.Event, want int) int64 {
	t.Helper()
	for _, event := range events {
		if event.Type != "session/state" {
			continue
		}
		var state session.Session
		if err := json.Unmarshal(event.Payload, &state); err != nil {
			t.Fatalf("decode state at seq %d: %v", event.Seq, err)
		}
		if len(state.Messages) == want {
			return event.Seq
		}
	}
	t.Fatalf("no session state with %d messages", want)
	return -1
}
