package memory

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"

	"code-agent/internal/identity"
	"code-agent/internal/session"
)

// Local CLI identities occupy the high half of the owner namespace so they
// cannot reuse ordinary web-user IDs or their memory approvals.
func OwnerForActor(actor identity.Actor) (uint, error) {
	if err := actor.Validate(); err != nil {
		return 0, err
	}
	actor.SessionID = ""
	digest := sha256.Sum256([]byte(actor.ScopeKey()))
	return uint(binary.BigEndian.Uint64(digest[:8])) | (uint(1) << 63), nil
}

func legacyMemoryOwner(events []session.Event) (uint, error) {
	actor, err := actorFromMemoryEvents(events)
	if err != nil {
		return 0, err
	}
	owner, err := OwnerForActor(actor)
	if err != nil {
		return 0, err
	}
	for _, event := range events {
		if event.Type == "session/deleted" {
			return 0, session.ErrSessionNotFound
		}
		if event.Type != "session/state" {
			continue
		}
		var state session.Session
		if json.Unmarshal(event.Payload, &state) != nil {
			return 0, ErrTrajectoryIntegrity
		}
		if state.Actor.SessionID == "" {
			continue
		}
		actual, err := OwnerForActor(state.Actor)
		if err != nil || actual != owner {
			return 0, ErrTrajectoryIntegrity
		}
	}
	return owner, nil
}

type cliTurnCompleted struct {
	StateEventID  string `json:"state_event_id"`
	StateChecksum string `json:"state_checksum"`
	Response      string `json:"response"`
}

func (m *LedgerMemory) CommitCLIOutcome(ctx context.Context, sessionID, response string) error {
	if strings.TrimSpace(response) == "" {
		return errors.New("empty CLI outcome")
	}
	for attempt := 0; attempt < 8; attempt++ {
		snapshot, err := session.ReadVerifiedSnapshot(ctx, m.ledger, sessionID)
		if err != nil {
			return err
		}
		if _, err := trajectoryOwner(snapshot.Events); err != nil {
			return err
		}
		var state *session.Event
		for i := len(snapshot.Events) - 1; i >= 0; i-- {
			if snapshot.Events[i].Type == "session/state" {
				state = &snapshot.Events[i]
				break
			}
		}
		if state == nil {
			return ErrIncompleteTrajectory
		}
		for _, event := range snapshot.Events {
			if event.Type != "memory/cli-turn-completed" {
				continue
			}
			var old cliTurnCompleted
			if json.Unmarshal(event.Payload, &old) != nil {
				return ErrTrajectoryIntegrity
			}
			if old.StateEventID == state.EventID {
				if old.StateChecksum != state.Checksum || old.Response != safeTrajectoryText(response) {
					return ErrTrajectoryIntegrity
				}
				return m.Commit(ctx, sessionID)
			}
		}
		_, err = m.ledger.Append(ctx, sessionID, int64(len(snapshot.Events)), "memory/cli-turn-completed", cliTurnCompleted{StateEventID: state.EventID, StateChecksum: state.Checksum, Response: safeTrajectoryText(response)})
		if errors.Is(err, session.ErrSequenceConflict) {
			continue
		}
		if err != nil {
			return err
		}
		return m.Commit(ctx, sessionID)
	}
	return session.ErrSequenceConflict
}

func buildLegacyTrajectory(snapshot session.LedgerSnapshot, sessionID string) (SessionTrajectory, error) {
	trajectory := SessionTrajectory{SessionID: sessionID, Outcome: trajectoryOutcomeIncomplete}
	branch := trajectoryBranch(snapshot.Events)
	for i := len(branch) - 1; i >= 0; i-- {
		terminal := branch[i]
		if terminal.Type != "memory/cli-turn-completed" {
			continue
		}
		var outcome cliTurnCompleted
		if json.Unmarshal(terminal.Payload, &outcome) != nil || strings.TrimSpace(outcome.Response) == "" {
			return trajectory, ErrTrajectoryIntegrity
		}
		var source *session.Event
		for j := i - 1; j >= 0; j-- {
			if branch[j].EventID == outcome.StateEventID {
				source = &branch[j]
				break
			}
		}
		if source == nil || source.Type != "session/state" || source.Checksum != outcome.StateChecksum {
			return trajectory, ErrTrajectoryIntegrity
		}
		var state session.Session
		if json.Unmarshal(source.Payload, &state) != nil {
			return trajectory, ErrTrajectoryIntegrity
		}
		for _, invocation := range state.Invocations {
			if invocation.Status != session.InvocationCommitted {
				return trajectory, ErrIncompleteTrajectory
			}
		}
		lines := []string{}
		for _, message := range state.Messages {
			if message.Role == session.RoleUser || message.Role == session.RoleAssistant {
				lines = append(lines, string(message.Role)+": "+safeTrajectoryText(message.Content))
			}
		}
		lines = append(lines, "assistant: "+safeTrajectoryText(outcome.Response), "Run outcome: completed")
		trajectory.FirstSeq, trajectory.LastSeq = source.Seq, terminal.Seq
		trajectory.Sources = []TrajectorySource{{Seq: source.Seq, EventID: source.EventID, Checksum: source.Checksum, Type: source.Type}, {Seq: terminal.Seq, EventID: terminal.EventID, Checksum: terminal.Checksum, Type: terminal.Type}}
		trajectory.Overview = boundTrajectoryOverview(strings.Join(lines, "\n"), defaultTrajectoryOverviewChars)
		trajectory.Complete, trajectory.Outcome = true, trajectoryOutcomeCompleted
		trajectory.SourceChecksum = checksumTrajectory(trajectory)
		return trajectory, nil
	}
	return trajectory, ErrIncompleteTrajectory
}
