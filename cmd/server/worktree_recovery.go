package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"
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
	Retained        bool   `json:"retained,omitempty"`
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
		Retained: tree.Retained,
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
			if event.Type != eventType {
				continue
			}
			var existing persistedWorktreeEvent
			if err := json.Unmarshal(event.Payload, &existing); err != nil {
				return fmt.Errorf("%w: invalid persisted worktree event at seq %d", session.ErrEventIntegrity, event.Seq)
			}
			if existing.RequestID != payload.RequestID {
				continue
			}
			if existing.ChildSessionID != payload.ChildSessionID || existing.LeaseID != payload.LeaseID {
				return fmt.Errorf("%w: conflicting persisted worktree identity at seq %d", session.ErrEventIntegrity, event.Seq)
			}
			if existing.Status == payload.Status {
				return nil
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
	if manager == nil {
		return errors.New("worktree manager is required")
	}
	restored, err := persistedActiveWorktrees(ctx, ledger)
	if err != nil {
		return err
	}
	return manager.RestoreChecked(restored)
}

func persistedActiveWorktrees(ctx context.Context, ledger session.EventLog) ([]worktree.Worktree, error) {
	if ledger == nil {
		return nil, errors.New("session ledger is required")
	}
	ids, err := ledger.SessionIDs(ctx)
	if err != nil {
		return nil, err
	}
	active := make(map[string]worktree.Worktree)
	for _, sessionID := range ids {
		if err := ledger.Verify(ctx, sessionID); err != nil {
			return nil, err
		}
		events, err := ledger.Events(ctx, sessionID)
		if err != nil {
			return nil, err
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
				return nil, fmt.Errorf("%w: invalid worktree recovery payload at seq %d", session.ErrEventIntegrity, event.Seq)
			}
			if strings.TrimSpace(payload.RequestID) == "" || strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Path) == "" || strings.TrimSpace(payload.Status) == "" {
				return nil, fmt.Errorf("%w: incomplete worktree recovery payload at seq %d", session.ErrEventIntegrity, event.Seq)
			}
			if strings.TrimSpace(payload.ParentSessionID) != strings.TrimSpace(sessionID) {
				return nil, fmt.Errorf("%w: worktree parent session mismatch at seq %d", session.ErrEventIntegrity, event.Seq)
			}
			key := sessionID + "\x00" + payload.RequestID
			if event.Type == persistedWorktreeTerminalEvent && (payload.Status == "completed" || payload.Status == "failed") {
				leaseExpiresAt, parseErr := parsePersistedWorktreeTime(payload.LeaseExpiresAt)
				if parseErr != nil {
					return nil, fmt.Errorf("%w: invalid retained worktree lease at seq %d: %v", session.ErrEventIntegrity, event.Seq, parseErr)
				}
				active[key] = worktree.Worktree{
					Name: payload.Name, Path: payload.Path, BaseRef: payload.BaseRef, Active: false,
					RequestID: payload.RequestID, ParentSessionID: payload.ParentSessionID, ChildSessionID: payload.ChildSessionID,
					LeaseID: payload.LeaseID, LeaseExpiresAt: leaseExpiresAt, Status: worktree.AgentWorktreeActive,
					Retained: payload.Retained,
				}
				continue
			}
			if event.Type == persistedWorktreeTerminalEvent || !payload.Active || payload.Status != worktree.AgentWorktreeActive {
				delete(active, key)
				continue
			}
			leaseExpiresAt, parseErr := parsePersistedWorktreeTime(payload.LeaseExpiresAt)
			if parseErr != nil {
				return nil, fmt.Errorf("%w: invalid worktree lease at seq %d: %v", session.ErrEventIntegrity, event.Seq, parseErr)
			}
			active[key] = worktree.Worktree{
				Name: payload.Name, Path: payload.Path, BaseRef: payload.BaseRef, Active: payload.Active,
				RequestID: payload.RequestID, ParentSessionID: payload.ParentSessionID, ChildSessionID: payload.ChildSessionID,
				LeaseID: payload.LeaseID, LeaseExpiresAt: leaseExpiresAt, Status: payload.Status,
				Retained: payload.Retained,
			}
		}
	}
	restored := make([]worktree.Worktree, 0, len(active))
	for _, tree := range active {
		restored = append(restored, tree)
	}
	sort.Slice(restored, func(i, j int) bool { return restored[i].Name < restored[j].Name })
	return restored, nil
}

func handlePersistedAgentLifecycle(ctx context.Context, ledger session.EventLog, manager *worktree.Manager, lifecycle *codeagentpb.AgentLifecycle) error {
	if ledger == nil || manager == nil || lifecycle == nil {
		return errors.New("agent lifecycle manager is not configured")
	}
	requestID := strings.TrimSpace(lifecycle.GetRequestId())
	childID := strings.TrimSpace(lifecycle.GetChildSessionId())
	leaseID := strings.TrimSpace(lifecycle.GetLeaseId())
	if requestID == "" || childID == "" {
		return errors.New("agent lifecycle request and child session are required")
	}
	status := strings.ToLower(strings.TrimSpace(lifecycle.GetStatus()))
	if status == "ok" {
		status = worktree.AgentWorktreeReleased
	}
	if status != worktree.AgentWorktreeReleased && status != "completed" && status != "failed" && status != "cancelled" && status != "reaped" {
		return errors.New("unsupported agent lifecycle status")
	}
	tree, ok := manager.FindAgent(requestID)
	if !ok {
		active, err := persistedActiveWorktrees(ctx, ledger)
		if err != nil {
			return err
		}
		for _, candidate := range active {
			if candidate.RequestID == requestID {
				tree, ok = candidate, true
				break
			}
		}
		if !ok {
			recorded, err := persistedAgentLifecycleRecorded(ctx, ledger, requestID, childID, leaseID, status)
			if err != nil {
				return err
			}
			if recorded {
				return nil
			}
			return errors.New("agent lifecycle request not found")
		}
	}
	if err := validatePersistedAgentIdentity(requestID, childID, leaseID, tree); err != nil {
		return err
	}
	if status == "completed" || status == "failed" {
		if _, found := manager.FindAgent(tree.RequestID); !found {
			if err := manager.RestoreChecked(append(manager.List(), tree)); err != nil {
				return err
			}
		}
		retained, err := manager.MarkAgentCompleted(tree.RequestID, lifecycle.GetReason())
		if err != nil {
			return err
		}
		tree = retained
	} else {
		discard := status != worktree.AgentWorktreeReleased
		if err := manager.CleanupAgent(ctx, tree.RequestID, discard, lifecycle.GetReason()); err != nil {
			return err
		}
	}
	tree.Active = false
	tree.Status = status
	if err := appendPersistedWorktreeEvent(ctx, ledger, tree.ParentSessionID, persistedWorktreeTerminalEvent, tree, lifecycle.GetReason()); err != nil {
		return fmt.Errorf("persist agent worktree lifecycle: %w", err)
	}
	return nil
}

func validatePersistedAgentIdentity(requestID, childID, leaseID string, tree worktree.Worktree) error {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(childID) == "" {
		return errors.New("agent lifecycle request and child session are required")
	}
	if tree.RequestID != requestID {
		return errors.New("agent lifecycle request does not match worktree")
	}
	if tree.ChildSessionID != childID {
		return errors.New("agent lifecycle child session does not match worktree")
	}
	if leaseID != "" && leaseID != tree.LeaseID {
		return errors.New("agent lifecycle lease does not match active worktree")
	}
	return nil
}

func persistedAgentLifecycleRecorded(ctx context.Context, ledger session.EventLog, requestID, childID, leaseID, status string) (bool, error) {
	ids, err := ledger.SessionIDs(ctx)
	if err != nil {
		return false, err
	}
	found := false
	for _, sessionID := range ids {
		if err := ledger.Verify(ctx, sessionID); err != nil {
			return false, err
		}
		events, err := ledger.Events(ctx, sessionID)
		if err != nil {
			return false, err
		}
		for i := len(events) - 1; i >= 0; i-- {
			event := events[i]
			if event.Type != persistedWorktreeTerminalEvent {
				continue
			}
			var payload persistedWorktreeEvent
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return false, fmt.Errorf("%w: invalid persisted worktree event at seq %d", session.ErrEventIntegrity, event.Seq)
			}
			if payload.RequestID != requestID {
				continue
			}
			found = true
			if payload.ChildSessionID != childID {
				return false, errors.New("agent lifecycle child session does not match recorded worktree")
			}
			if leaseID != "" && payload.LeaseID != leaseID {
				return false, errors.New("agent lifecycle lease does not match recorded worktree")
			}
			if payload.Status == status {
				return true, nil
			}
			return false, fmt.Errorf("agent lifecycle status conflicts with recorded %s", payload.Status)
		}
	}
	if !found {
		return false, nil
	}
	return false, errors.New("agent lifecycle request is not recoverable")
}

func parsePersistedWorktreeTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
}
