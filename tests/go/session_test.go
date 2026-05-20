package codeagent_test

import (
	"context"
	"path/filepath"
	"testing"

	"code-agent/internal/session"
)

func TestSessionManagerTracksMessages(t *testing.T) {
	manager := session.NewManager(nil)
	manager.NewSession(t.TempDir())

	current := manager.Append(session.RoleUser, "hello")
	if current.ID == "" {
		t.Fatal("session id should be assigned")
	}

	messages := manager.Messages()
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "hello" {
		t.Fatalf("unexpected message content: %+v", messages[0])
	}
}

func TestSQLiteStorePersistsSessions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite")

	store := session.NewSQLiteStore(path)
	manager := session.NewManager(store)
	created := manager.NewSession(t.TempDir())
	manager.Append(session.RoleUser, "persist me")
	manager.Append(session.RoleAssistant, "persisted")
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reloadedStore := session.NewSQLiteStore(path)
	reloaded := session.NewManager(reloadedStore)
	loaded, err := reloaded.Load(ctx, created.ID)
	if err != nil {
		t.Fatalf("load persisted session: %v", err)
	}
	defer reloadedStore.Close()
	if loaded.ID != created.ID {
		t.Fatalf("loaded wrong session id: %s", loaded.ID)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "persist me" {
		t.Fatalf("unexpected first message: %+v", loaded.Messages[0])
	}
}

func TestManagerResumeLatest(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	manager := session.NewManager(store)

	first := manager.NewSession("first")
	manager.Append(session.RoleUser, "first message")
	second := manager.NewSession("second")
	manager.Append(session.RoleUser, "second message")

	if second.ID == first.ID {
		t.Fatal("sessions should have unique ids")
	}

	resumed, ok, err := manager.ResumeLatest(ctx)
	if err != nil {
		t.Fatalf("resume latest: %v", err)
	}
	if !ok {
		t.Fatal("expected previous session")
	}
	if resumed.ID != first.ID {
		t.Fatalf("expected to resume first session, got %s", resumed.ID)
	}
}
