package memory

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"code-agent/internal/session"
)

// FrozenView delegates all writes to the canonical Ledger while historical reads
// use only verified prefixes selected before the trial began.
type FrozenView struct {
	live   *LedgerMemory
	reader *LedgerMemory
	scope  *frozenLedger
}

func NewFrozenView(live *LedgerMemory, refs []session.FrozenMemoryReference, workspace string, expectedOwner uint) (*FrozenView, error) {
	if live == nil || live.ledger == nil {
		return nil, errors.New("Frozen Memory requires a Ledger")
	}
	ctx := context.Background()
	targetHead, err := frozenGitHead(ctx, workspace)
	if err != nil {
		return nil, err
	}
	view := &frozenLedger{EventLog: live.ledger, snapshots: map[string]session.LedgerSnapshot{}, workspace: workspace, owner: expectedOwner}
	var selectedOwner uint
	for _, ref := range refs {
		if _, exists := view.snapshots[ref.SessionID]; exists {
			return nil, errors.New("Frozen Memory has duplicate source prefixes")
		}
		snapshot, err := session.ReadVerifiedSnapshot(ctx, live.ledger, ref.SessionID)
		if err != nil {
			return nil, err
		}
		var pin *session.Event
		for _, event := range snapshot.Events {
			if event.EventID == ref.EventID && event.Checksum == ref.Checksum {
				copy := event
				pin = &copy
				break
			}
		}
		if pin == nil {
			return nil, errors.New("Frozen Memory source pin mismatch")
		}
		owner, err := trajectoryOwner(snapshot.Events)
		if len(snapshot.Events) > 0 && snapshot.Events[0].Type == "memory/catalog-created" {
			var header struct {
				Owner uint `json:"owner_id"`
			}
			if json.Unmarshal(snapshot.Events[0].Payload, &header) != nil || header.Owner == 0 {
				return nil, ErrTrajectoryIntegrity
			}
			owner, err = header.Owner, nil
		} else if err == nil {
			var created struct {
				WorkingDir string `json:"working_dir"`
			}
			if json.Unmarshal(snapshot.Events[0].Payload, &created) != nil {
				return nil, ErrTrajectoryIntegrity
			}
			if snapshot.Events[0].Type == "session/state" {
				var state session.Session
				if json.Unmarshal(snapshot.Events[0].Payload, &state) != nil {
					return nil, ErrTrajectoryIntegrity
				}
				created.WorkingDir = state.WorkingDir
			}
			head, headErr := frozenGitHead(ctx, created.WorkingDir)
			if headErr != nil || head != targetHead {
				return nil, errors.New("Frozen Memory repository mismatch")
			}
		}
		if err != nil {
			return nil, err
		}
		if owner != expectedOwner || selectedOwner != 0 && owner != selectedOwner {
			return nil, errors.New("Frozen Memory owner mismatch")
		}
		selectedOwner = owner
		prefix, err := snapshot.Prefix(pin.Seq + 1)
		if err != nil {
			return nil, err
		}
		view.snapshots[ref.SessionID] = prefix
	}
	reader := NewLedgerMemory(view)
	// Each selected trial retains its own write namespace. Resolve identities
	// from the frozen prefixes rather than treating the first source as all sources.
	repositories := map[string]string{}
	for _, ref := range refs {
		if view.snapshots[ref.SessionID].Events[0].Type == "memory/catalog-created" {
			continue
		}
		repository, err := reader.repositoryKey(ctx, expectedOwner, ref.SessionID)
		if err != nil {
			return nil, err
		}
		repositories[ref.SessionID] = repository
	}
	reader.frozenSourceRepositories = repositories
	for _, ref := range refs {
		if key := repositories[ref.SessionID]; key != "" {
			reader.frozenRepository = key
			break
		}
	}
	_, entries, _, err := reader.catalog(ctx, expectedOwner)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Deleted {
			continue
		}
		for _, origin := range entry.Origins {
			prefix, selected := view.snapshots[origin.SessionID]
			if !selected || validateOriginSource(prefix, expectedOwner, origin) != nil {
				return nil, errors.New("Frozen Memory catalog source is outside selected prefixes")
			}
		}
	}
	return &FrozenView{live: live, reader: reader, scope: view}, nil
}

func frozenGitHead(ctx context.Context, directory string) (string, error) {
	body, err := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return "", errors.New("Frozen Memory repository identity unavailable")
	}
	return strings.TrimSpace(string(body)), nil
}

func (v *FrozenView) Commit(ctx context.Context, id string) error { return v.live.Commit(ctx, id) }
func (v *FrozenView) Recall(ctx context.Context, owner uint, query string, maxTokens int) (string, error) {
	return v.reader.Recall(ctx, owner, query, maxTokens)
}
func (v *FrozenView) RecallWithOptions(ctx context.Context, owner uint, query session.MemoryQuery) (string, error) {
	ctx = context.WithValue(ctx, frozenMetadataSession{}, query.TargetSessionID)
	return v.reader.RecallWithOptions(ctx, owner, query)
}
func (v *FrozenView) Manage(ctx context.Context, id string, command session.MemoryCommand) (string, error) {
	if command.Action == "read" || command.Action == "list" {
		ctx = context.WithValue(ctx, frozenMetadataSession{}, id)
		return v.reader.Manage(ctx, id, command)
	}
	_, root, err := v.scope.trialSnapshot(ctx, id)
	if err != nil {
		return "", err
	}
	return v.live.manageWithWriteGuard(ctx, id, command, func(previous Experience) error {
		if len(previous.Origins) == 0 {
			return ErrTrajectoryIntegrity
		}
		for _, origin := range previous.Origins {
			_, sourceRoot, err := v.scope.trialSnapshot(ctx, origin.SessionID)
			if err != nil || sourceRoot != root {
				return errors.New("Frozen Memory writes cannot modify prior trials")
			}
		}
		return nil
	})
}

type frozenLedger struct {
	session.EventLog
	snapshots map[string]session.LedgerSnapshot
	workspace string
	owner     uint
}

type frozenMetadataSession struct{}

func (l *frozenLedger) SessionIDs(context.Context) ([]string, error) {
	ids := make([]string, 0, len(l.snapshots))
	for id := range l.snapshots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
func (l *frozenLedger) Events(ctx context.Context, id string) ([]session.Event, error) {
	if snapshot, ok := l.snapshots[id]; ok {
		return append([]session.Event(nil), snapshot.Events...), nil
	}
	if target, _ := ctx.Value(frozenMetadataSession{}).(string); target == "" || target != id {
		return nil, nil
	}
	snapshot, _, err := l.trialSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	// Current trial identity is needed for scoping, never for historical content.
	return append([]session.Event(nil), snapshot.Events[:1]...), nil
}

func (l *frozenLedger) trialSnapshot(ctx context.Context, id string) (session.LedgerSnapshot, string, error) {
	snapshot, err := session.ReadVerifiedSnapshot(ctx, l.EventLog, id)
	if err != nil {
		return session.LedgerSnapshot{}, "", err
	}
	owner, err := trajectoryOwner(snapshot.Events)
	if err != nil || owner != l.owner {
		return session.LedgerSnapshot{}, "", session.ErrSessionNotFound
	}
	actor, err := actorFromMemoryEvents(snapshot.Events)
	if err != nil {
		return session.LedgerSnapshot{}, "", err
	}
	root, err := session.ModelTaskRoot(ctx, l.EventLog, actor)
	if err != nil {
		return session.LedgerSnapshot{}, "", err
	}
	rootSnapshot, err := session.ReadVerifiedSnapshot(ctx, l.EventLog, root)
	if err != nil {
		return session.LedgerSnapshot{}, "", err
	}
	binding, err := session.ReadEvaluation(rootSnapshot.Events)
	if err != nil || binding == nil {
		return session.LedgerSnapshot{}, "", session.ErrSessionNotFound
	}
	relative, err := filepath.Rel(l.workspace, binding.Workspace)
	if err != nil || relative != "." {
		return session.LedgerSnapshot{}, "", session.ErrSessionNotFound
	}
	return snapshot, root, nil
}
func (l *frozenLedger) Verify(ctx context.Context, id string) error {
	_, err := session.ReadVerifiedSnapshot(ctx, l, id)
	return err
}
func (l *frozenLedger) Surface(ctx context.Context, id string) ([]session.Event, error) {
	snapshot, err := session.ReadVerifiedSnapshot(ctx, l, id)
	return snapshot.Surface, err
}
