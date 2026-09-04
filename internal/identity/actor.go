// Package identity defines the authenticated actor carried across Harness
// and orchestrator boundaries.
package identity

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

const SchemaVersion uint32 = 1

var (
	ErrInvalidActor           = errors.New("invalid actor identity")
	ErrSessionBindingMismatch = errors.New("actor is already bound to another session")
)

// Actor is the non-secret identity of the authenticated caller. It is safe to
// persist and include in audit metadata; credentials never belong here.
type Actor struct {
	SchemaVersion uint32   `json:"schema_version"`
	ActorID       string   `json:"actor_id"`
	Subject       string   `json:"subject"`
	TenantID      string   `json:"tenant_id"`
	Roles         []string `json:"roles"`
	SessionID     string   `json:"session_id"`
}

// Default returns the local CLI actor used when no external identity provider
// is configured. The identity is still explicit and session-bound.
func Default() Actor {
	return Actor{
		SchemaVersion: SchemaVersion,
		ActorID:       "actor:local",
		Subject:       "local",
		TenantID:      "tenant:local",
		Roles:         []string{"LOCAL"},
	}
}

func (a Actor) Validate() error {
	if a.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema version %d", ErrInvalidActor, a.SchemaVersion)
	}
	for name, value := range map[string]string{
		"actor_id":   a.ActorID,
		"subject":    a.Subject,
		"tenant_id":  a.TenantID,
		"session_id": a.SessionID,
	} {
		if err := validateField(name, value); err != nil {
			return err
		}
	}
	if len(a.Roles) == 0 {
		return fmt.Errorf("%w: at least one role is required", ErrInvalidActor)
	}
	for _, role := range a.Roles {
		if err := validateField("role", role); err != nil {
			return err
		}
	}
	return nil
}

func validateField(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidActor, name)
	}
	if len(value) > 256 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s is invalid", ErrInvalidActor, name)
	}
	return nil
}

// BindSession returns a copy bound to one durable session. Rebinding an actor
// to a different session is rejected so a caller cannot replay approvals or
// history across sessions.
func (a Actor) BindSession(sessionID string) (Actor, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Actor{}, fmt.Errorf("%w: session_id is required", ErrInvalidActor)
	}
	if a.SessionID != "" && a.SessionID != sessionID {
		return Actor{}, ErrSessionBindingMismatch
	}
	a.SchemaVersion = SchemaVersion
	a.SessionID = sessionID
	a.ActorID = strings.TrimSpace(a.ActorID)
	a.Subject = strings.TrimSpace(a.Subject)
	a.TenantID = strings.TrimSpace(a.TenantID)
	a.Roles = normalizeRoles(a.Roles)
	if err := a.Validate(); err != nil {
		return Actor{}, err
	}
	return a, nil
}

func normalizeRoles(roles []string) []string {
	seen := make(map[string]struct{}, len(roles))
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.ToUpper(strings.TrimSpace(role))
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	return out
}

// ScopeKey is a stable, non-secret key for session-scoped authorization state.
// All trust-bearing fields participate so changing a subject, tenant, or role
// set cannot reuse approvals that belong to a different identity.
func (a Actor) ScopeKey() string {
	roles := normalizeRoles(a.Roles)
	sort.Strings(roles)
	return strings.Join([]string{a.ActorID, a.Subject, a.TenantID, strings.Join(roles, ","), a.SessionID}, "\x00")
}
