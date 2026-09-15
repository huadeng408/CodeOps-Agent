package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"code-agent/internal/session"
)

const trajectoryCommittedEvent = "memory/trajectory-committed"

var ErrIncompleteTrajectory = errors.New("session trajectory is incomplete")

type trajectoryCommit struct {
	SchemaVersion int               `json:"schema_version"`
	OwnerID       uint              `json:"owner_id"`
	Trajectory    SessionTrajectory `json:"trajectory"`
}

// LedgerMemory owns no writable side store. Commits are immutable ledger facts;
// recall rebuilds the owner-scoped read projection using the current branch.
type LedgerMemory struct {
	ledger    session.EventLog
	workbench *session.Workbench
}

var _ session.SessionMemory = (*LedgerMemory)(nil)

func NewLedgerMemory(ledger session.EventLog) *LedgerMemory {
	return &LedgerMemory{ledger: ledger, workbench: session.NewWorkbench(ledger, nil)}
}

func (m *LedgerMemory) Commit(ctx context.Context, sessionID string) error {
	if m == nil || m.ledger == nil {
		return errors.New("session ledger is required")
	}
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, err := session.ReadVerifiedSnapshot(ctx, m.ledger, sessionID)
		if err != nil {
			return err
		}
		ownerID, err := trajectoryOwner(snapshot.Events)
		if err != nil {
			return err
		}
		trajectory, err := buildSessionTrajectory(snapshot, sessionID, TrajectoryOptions{})
		if err != nil {
			return err
		}
		if !trajectory.Complete {
			return ErrIncompleteTrajectory
		}
		previous, _, err := latestTrajectoryCommit(snapshot.Events, ownerID)
		if err != nil {
			return err
		}
		if previous != nil && previous.Trajectory.SourceChecksum == trajectory.SourceChecksum {
			return nil
		}
		_, err = m.ledger.Append(ctx, sessionID, int64(len(snapshot.Events)), trajectoryCommittedEvent, trajectoryCommit{
			SchemaVersion: 1, OwnerID: ownerID, Trajectory: trajectory,
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, session.ErrSequenceConflict) {
			return err
		}
	}
	return session.ErrSequenceConflict
}

// Recall accepts only the owner issued by the Harness, never model parameters.
// The lexical ranker and token estimator are shared with legacy file memory.
func (m *LedgerMemory) Recall(ctx context.Context, ownerID uint, query string, maxTokens int) (string, error) {
	if m == nil || m.ledger == nil {
		return "", errors.New("session ledger is required")
	}
	if ownerID == 0 {
		return "", session.ErrSessionOwnerRequired
	}
	if strings.TrimSpace(query) == "" || len(query) > 8000 || maxTokens < 0 || maxTokens > 8000 {
		return "", errors.New("invalid memory recall query or budget")
	}
	if maxTokens == 0 {
		maxTokens = 1200
	}
	views, err := m.workbench.List(ctx, ownerID)
	if err != nil {
		return "", err
	}
	items := make([]Memory, 0, len(views))
	for _, view := range views {
		snapshot, err := session.ReadVerifiedSnapshot(ctx, m.ledger, view.ID)
		if err != nil {
			return "", err
		}
		actualOwner, err := trajectoryOwner(snapshot.Events)
		if err != nil || actualOwner != ownerID {
			return "", session.ErrSessionNotFound
		}
		commit, event, err := latestTrajectoryCommit(snapshot.Events, ownerID)
		if err != nil {
			return "", err
		}
		if commit == nil {
			continue
		}
		current, err := buildSessionTrajectory(snapshot, view.ID, TrajectoryOptions{})
		if err != nil {
			return "", err
		}
		// A rewind, compaction, new turn, or unknown result invalidates the old
		// projection. Never recall a commit that no longer matches live facts.
		if !current.Complete || current.SourceChecksum != commit.Trajectory.SourceChecksum {
			continue
		}
		item := trajectoryMemory(current)
		item.CreatedAt, item.UpdatedAt = event.CreatedAt, event.CreatedAt
		item, err = normalizeMemory(item)
		if err != nil {
			return "", err
		}
		item.Checksum = checksumMemory(item)
		items = append(items, item)
	}
	projection := &Manager{items: items}
	result, err := projection.Recall(query, RecallOptions{Namespace: "user", Kind: "trajectories", Limit: 5, MaxTokens: maxTokens})
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	return string(encoded), err
}

func trajectoryOwner(events []session.Event) (uint, error) {
	if len(events) == 0 || events[0].Type != "session/created" {
		return 0, session.ErrSessionOwnerRequired
	}
	var created struct {
		OwnerID uint `json:"owner_id"`
	}
	if err := json.Unmarshal(events[0].Payload, &created); err != nil || created.OwnerID == 0 {
		return 0, session.ErrSessionOwnerRequired
	}
	for _, event := range events {
		if event.Type == "session/deleted" {
			return 0, session.ErrSessionNotFound
		}
	}
	return created.OwnerID, nil
}

func latestTrajectoryCommit(events []session.Event, ownerID uint) (*trajectoryCommit, session.Event, error) {
	branch := trajectoryBranch(events)
	for i := len(branch) - 1; i >= 0; i-- {
		event := branch[i]
		if event.Type != trajectoryCommittedEvent {
			continue
		}
		var commit trajectoryCommit
		if err := json.Unmarshal(event.Payload, &commit); err != nil || commit.SchemaVersion != 1 ||
			commit.OwnerID != ownerID || commit.Trajectory.SessionID != event.SessionID ||
			!commit.Trajectory.Complete || commit.Trajectory.LastSeq >= event.Seq ||
			commit.Trajectory.SourceChecksum != checksumTrajectory(commit.Trajectory) {
			return nil, session.Event{}, fmt.Errorf("%w: invalid memory commit at seq %d", ErrTrajectoryIntegrity, event.Seq)
		}
		return &commit, event, nil
	}
	return nil, session.Event{}, nil
}
