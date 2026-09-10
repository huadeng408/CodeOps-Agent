package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"code-agent/internal/session"
	"code-agent/internal/worktree"
)

const (
	persistedWorktreeActiveEvent   = "agent/worktree-active"
	persistedWorktreeTerminalEvent = "agent/worktree-terminal"
)

type persistedWorktreeEvent struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	BaseRef         string `json:"base_ref,omitempty"`
	Active          bool   `json:"active"`
	RequestID       string `json:"request_id"`
	ParentSessionID string `json:"parent_session_id"`
	ChildSessionID  string `json:"child_session_id"`
	LeaseID         string `json:"lease_id"`
	LeaseExpiresAt  string `json:"lease_expires_at"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
}

func appendPersistedWorktreeEvent(ctx context.Context, ledger session.EventLog, sessionID, eventType string, tree worktree.Worktree, reason string) error {
	if ledger == nil {
		return errors.New("session ledger is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	payload := persistedWorktreeEvent{
		Name: tree.Name, Path: tree.Path, BaseRef: tree.BaseRef, Active: tree.Active,
		RequestID: tree.RequestID, ParentSessionID: tree.ParentSessionID, ChildSessionID: tree.ChildSessionID,
		LeaseID: tree.LeaseID, LeaseExpiresAt: tree.LeaseExpiresAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Status: tree.Status, Reason: strings.TrimSpace(reason),
	}
	if eventType == persistedWorktreeTerminalEvent {
		payload.Active = false
	}
	for attempt := 0; attempt < 16; attempt++ {
		events, err := ledger.Events(ctx, sessionID)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return session.ErrSessionNotFound
		}
		for _, event := range events {
			if event.Type == "session/deleted" {
				return session.ErrSessionNotFound
			}
		}
		if _, err := ledger.Append(ctx, sessionID, int64(len(events)), eventType, payload); err == nil {
			return nil
		} else if !errors.Is(err, session.ErrSequenceConflict) {
			return err
		}
	}
	return session.ErrSequenceConflict
}

func restorePersistedWorktrees(ctx context.Context, ledger session.EventLog, manager *worktree.Manager) error {
	if ledger == nil {
		return errors.New("session ledger is required")
	}
	if manager == nil {
		return errors.New("worktree manager is required")
	}
	ids, err := ledger.SessionIDs(ctx)
	if err != nil {
		return err
	}
	active := make(map[string]worktree.Worktree)
	for _, sessionID := range ids {
		if err := ledger.Verify(ctx, sessionID); err != nil {
			return err
		}
		events, err := ledger.Events(ctx, sessionID)
		if err != nil {
			return err
		}
		deleted := false
		for _, event := range events {
			if event.Type == "session/deleted" {
				deleted = true
				break
			}
		}
		if deleted {
			continue
		}
		for _, event := range events {
			if event.Type != persistedWorktreeActiveEvent && event.Type != persistedWorktreeTerminalEvent {
				continue
			}
			var payload persistedWorktreeEvent
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return fmt.Errorf("%w: invalid worktree recovery payload at seq %d", session.ErrEventIntegrity, event.Seq)
			}
			if strings.TrimSpace(payload.RequestID) == "" || strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Path) == "" || strings.TrimSpace(payload.Status) == "" {
				return fmt.Errorf("%w: incomplete worktree recovery payload at seq %d", session.ErrEventIntegrity, event.Seq)
			}
			if strings.TrimSpace(payload.ParentSessionID) != strings.TrimSpace(sessionID) {
				return fmt.Errorf("%w: worktree parent session mismatch at seq %d", session.ErrEventIntegrity, event.Seq)
			}
			key := sessionID + "\x00" + payload.RequestID
			if event.Type == persistedWorktreeTerminalEvent || !payload.Active || payload.Status != worktree.AgentWorktreeActive {
				delete(active, key)
				continue
			}
			leaseExpiresAt, parseErr := parsePersistedWorktreeTime(payload.LeaseExpiresAt)
			if parseErr != nil {
				return fmt.Errorf("%w: invalid worktree lease at seq %d: %v", session.ErrEventIntegrity, event.Seq, parseErr)
			}
			active[key] = worktree.Worktree{
				Name: payload.Name, Path: payload.Path, BaseRef: payload.BaseRef, Active: payload.Active,
				RequestID: payload.RequestID, ParentSessionID: payload.ParentSessionID, ChildSessionID: payload.ChildSessionID,
				LeaseID: payload.LeaseID, LeaseExpiresAt: leaseExpiresAt, Status: payload.Status,
			}
		}
	}
	restored := make([]worktree.Worktree, 0, len(active))
	for _, tree := range active {
		restored = append(restored, tree)
	}
	sort.Slice(restored, func(i, j int) bool { return restored[i].Name < restored[j].Name })
	if err := manager.RestoreChecked(restored); err != nil {
		return err
	}
	return nil
}

func parsePersistedWorktreeTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
}
