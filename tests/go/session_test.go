package codeagent_test

import (
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
