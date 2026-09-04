package identity

import (
	"errors"
	"testing"
)

func TestActorIdentityBindsToExactlyOneSession(t *testing.T) {
	actor := Actor{
		SchemaVersion: SchemaVersion,
		ActorID:       "user:42",
		Subject:       "alice",
		TenantID:      "org:7",
		Roles:         []string{"USER"},
	}

	bound, err := actor.BindSession("session-1")
	if err != nil {
		t.Fatalf("BindSession() error = %v", err)
	}
	if err := bound.Validate(); err != nil {
		t.Fatalf("bound actor should validate: %v", err)
	}
	if bound.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want session-1", bound.SessionID)
	}
	if _, err := bound.BindSession("session-2"); !errors.Is(err, ErrSessionBindingMismatch) {
		t.Fatalf("rebinding should fail closed, got %v", err)
	}
}

func TestActorIdentityRejectsMissingTrustFields(t *testing.T) {
	cases := []Actor{
		{ActorID: "user:42", Subject: "alice", TenantID: "org:7", SessionID: "s"},
		{SchemaVersion: SchemaVersion, Subject: "alice", TenantID: "org:7", SessionID: "s"},
		{SchemaVersion: SchemaVersion, ActorID: "user:42", TenantID: "org:7", SessionID: "s"},
		{SchemaVersion: SchemaVersion, ActorID: "user:42", Subject: "alice", SessionID: "s"},
		{SchemaVersion: SchemaVersion, ActorID: "user:42", Subject: "alice", TenantID: "org:7"},
	}
	for idx, actor := range cases {
		if err := actor.Validate(); err == nil {
			t.Fatalf("case %d should fail validation", idx)
		}
	}
}

func TestActorScopeIncludesAllTrustFields(t *testing.T) {
	base := Actor{
		SchemaVersion: SchemaVersion,
		ActorID:       "user:42",
		Subject:       "alice",
		TenantID:      "org:7",
		Roles:         []string{"USER", "REVIEWER"},
		SessionID:     "session-1",
	}
	variants := []Actor{
		{SchemaVersion: SchemaVersion, ActorID: "user:42", Subject: "mallory", TenantID: "org:7", Roles: []string{"USER", "REVIEWER"}, SessionID: "session-1"},
		{SchemaVersion: SchemaVersion, ActorID: "user:42", Subject: "alice", TenantID: "org:8", Roles: []string{"USER", "REVIEWER"}, SessionID: "session-1"},
		{SchemaVersion: SchemaVersion, ActorID: "user:42", Subject: "alice", TenantID: "org:7", Roles: []string{"USER"}, SessionID: "session-1"},
	}
	for idx, variant := range variants {
		if variant.ScopeKey() == base.ScopeKey() {
			t.Fatalf("variant %d reused the base actor scope", idx)
		}
	}
	ordered := base
	ordered.Roles = []string{"REVIEWER", "USER"}
	if ordered.ScopeKey() != base.ScopeKey() {
		t.Fatal("role ordering should not change actor scope")
	}
}
