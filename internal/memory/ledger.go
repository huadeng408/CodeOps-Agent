package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"code-agent/internal/session"
	"code-agent/internal/telemetry/genai"
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
	reflector MemoryReflector
	tracerMu  sync.RWMutex
	tracer    genai.Tracer
}

var _ session.SessionMemory = (*LedgerMemory)(nil)

func NewLedgerMemory(ledger session.EventLog, reflectors ...MemoryReflector) *LedgerMemory {
	m := &LedgerMemory{ledger: ledger, workbench: session.NewWorkbench(ledger, nil)}
	if len(reflectors) > 0 {
		m.reflector = reflectors[0]
	}
	return m
}

// SetTracer wires lifecycle spans without changing Memory's ledger ownership.
func (m *LedgerMemory) SetTracer(tracer genai.Tracer) {
	if m == nil {
		return
	}
	m.tracerMu.Lock()
	m.tracer = tracer
	m.tracerMu.Unlock()
}

func (m *LedgerMemory) startSpan(ctx context.Context, name, operation string) (context.Context, genai.Span) {
	if m == nil {
		return ctx, nil
	}
	m.tracerMu.RLock()
	tracer := m.tracer
	m.tracerMu.RUnlock()
	if tracer == nil {
		return ctx, nil
	}
	return tracer.StartSpan(ctx, name, operation, genai.SystemGenAI)
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
			return m.reflectTrajectory(ctx, snapshot, ownerID, *previous)
		}
		_, err = m.ledger.Append(ctx, sessionID, int64(len(snapshot.Events)), trajectoryCommittedEvent, trajectoryCommit{
			SchemaVersion: 1, OwnerID: ownerID, Trajectory: trajectory,
		})
		if err == nil {
			snapshot, err = session.ReadVerifiedSnapshot(ctx, m.ledger, sessionID)
			if err != nil {
				return err
			}
			return m.reflectTrajectory(ctx, snapshot, ownerID, trajectoryCommit{SchemaVersion: 1, OwnerID: ownerID, Trajectory: trajectory})
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
	return m.RecallWithOptions(ctx, ownerID, session.MemoryQuery{Query: query, MaxTokens: maxTokens})
}

func (m *LedgerMemory) RecallWithOptions(ctx context.Context, ownerID uint, options session.MemoryQuery) (output string, err error) {
	if options.SourceSessionID != "" {
		ctx = genai.WithEvalInstance(ctx, options.SourceSessionID)
	}
	ctx, span := m.startSpan(ctx, "memory.recall", genai.OperationRetrieve)
	if span != nil {
		defer func() {
			if err != nil {
				genai.MarkSpanError(span)
			}
			span.End()
		}()
	}
	query, maxTokens := options.Query, options.MaxTokens
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
	views, err := m.sourceSessions(ctx, ownerID)
	if err != nil {
		return "", err
	}
	items := make([]Memory, 0, len(views))
	for _, view := range views {
		if options.SourceSessionID != "" && view.ID != options.SourceSessionID {
			continue
		}
		snapshot, err := session.ReadVerifiedSnapshot(ctx, m.ledger, view.ID)
		if err != nil {
			return "", err
		}
		actualOwner, err := trajectoryOwner(snapshot.Events)
		if err != nil || actualOwner != ownerID {
			return "", session.ErrSessionNotFound
		}
		err = visitActiveTrajectoryCommits(snapshot, ownerID, func(commit trajectoryCommit, event session.Event) (bool, error) {
			item := trajectoryMemory(commit.Trajectory)
			item.CreatedAt, item.UpdatedAt = event.CreatedAt, event.CreatedAt
			item, err = normalizeMemory(item)
			if err != nil {
				return false, err
			}
			item.Checksum = checksumMemory(item)
			items = append(items, item)
			return true, nil
		})
		if err != nil {
			return "", err
		}
	}
	experiences, err := m.experienceItems(ctx, ownerID, options.Detail, options.SourceSessionID)
	if err != nil {
		return "", err
	}
	items = append(items, experiences...)
	if options.TargetSessionID != "" {
		items, err = m.repositoryItems(ctx, ownerID, options.TargetSessionID, items)
		if err != nil {
			return "", err
		}
	}
	if options.Detail != "" {
		for i := range items {
			if items[i].Kind == "trajectories" {
				items[i].Detail = options.Detail
				if options.Detail == "abstract" {
					items[i].Content = summary(items[i].Content, 240)
				}
				items[i].Checksum = checksumMemory(items[i])
			}
		}
	}
	projection := &Manager{items: items}
	result, err := projection.Recall(query, RecallOptions{Namespace: "user", Kind: options.Kind, Detail: options.Detail, Limit: 5, MaxTokens: maxTokens})
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	return string(encoded), err
}

func (m *LedgerMemory) repositoryItems(ctx context.Context, owner uint, targetSessionID string, items []Memory) ([]Memory, error) {
	target, err := m.repositoryKey(ctx, owner, targetSessionID)
	if err != nil {
		return nil, err
	}
	filtered := make([]Memory, 0, len(items))
	repositories := map[string]string{targetSessionID: target}
	for _, item := range items {
		source, found := repositories[item.SessionID]
		if !found {
			source, err = m.repositoryKey(ctx, owner, item.SessionID)
			if err != nil {
				return nil, err
			}
			repositories[item.SessionID] = source
		}
		if source == target {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (m *LedgerMemory) repositoryKey(ctx context.Context, owner uint, id string) (string, error) {
	snapshot, err := session.ReadVerifiedSnapshot(ctx, m.ledger, id)
	if err != nil {
		return "", err
	}
	actual, err := trajectoryOwner(snapshot.Events)
	if err != nil || actual != owner {
		return "", session.ErrSessionNotFound
	}
	var directory string
	if snapshot.Events[0].Type == "session/created" {
		var created struct {
			WorkingDir string `json:"working_dir"`
		}
		if err := json.Unmarshal(snapshot.Events[0].Payload, &created); err != nil {
			return "", err
		}
		directory = created.WorkingDir
	} else {
		// A CLI cd changes tool cwd, not the Session's original repository.
		for i := 0; i < len(snapshot.Events); i++ {
			if snapshot.Events[i].Type == "session/state" {
				var state session.Session
				if err := json.Unmarshal(snapshot.Events[i].Payload, &state); err != nil {
					return "", err
				}
				directory = state.WorkingDir
				break
			}
		}
	}
	if directory == "" || !filepath.IsAbs(directory) {
		return "", errors.New("memory repository scope is unavailable")
	}
	directory = filepath.Clean(directory)
	if runtime.GOOS == "windows" {
		directory = strings.ToLower(directory)
	}
	return trajectoryDigest(directory), nil
}

func trajectoryOwner(events []session.Event) (uint, error) {
	if len(events) > 0 && (events[0].Type == "session/state" || events[0].Type == "legacy/import") {
		return legacyMemoryOwner(events)
	}
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

func activeTrajectoryCommit(snapshot session.LedgerSnapshot, ownerID uint) (*trajectoryCommit, session.Event, error) {
	var commit *trajectoryCommit
	var event session.Event
	err := visitActiveTrajectoryCommits(snapshot, ownerID, func(candidate trajectoryCommit, source session.Event) (bool, error) {
		commit, event = &candidate, source
		return false, nil
	})
	return commit, event, err
}

func visitActiveTrajectoryCommits(snapshot session.LedgerSnapshot, ownerID uint, visit func(trajectoryCommit, session.Event) (bool, error)) error {
	if _, _, err := latestTrajectoryCommit(snapshot.Events, ownerID); err != nil {
		return err
	}
	covered, err := compactedSurfaceSources(snapshot)
	if err != nil {
		return err
	}
	branch := trajectoryBranch(snapshot.Events)
	branchIDs := make(map[string]bool)
	for _, e := range branch {
		branchIDs[e.EventID] = true
	}
	for i := len(branch) - 1; i >= 0; i-- {
		e := branch[i]
		if e.Type != trajectoryCommittedEvent {
			continue
		}
		var commit trajectoryCommit
		if json.Unmarshal(e.Payload, &commit) != nil || commit.SchemaVersion != 1 || commit.OwnerID != ownerID ||
			commit.Trajectory.SessionID != e.SessionID || !commit.Trajectory.Complete || commit.Trajectory.LastSeq >= e.Seq ||
			commit.Trajectory.SourceChecksum != checksumTrajectory(commit.Trajectory) || !trajectorySourcesValid(snapshot.Events, commit.Trajectory.Sources, e.Seq) {
			return ErrTrajectoryIntegrity
		}
		valid := true
		for _, source := range commit.Trajectory.Sources {
			fact, sourceErr := resolveTrajectorySource(snapshot.Events, source)
			if sourceErr != nil {
				return sourceErr
			}
			// Compaction ancestors remain evidence; rewound facts are not covered.
			if !branchIDs[fact.EventID] || fact.SurfaceOp != nil && !covered[fact.EventID] {
				valid = false
				break
			}
		}
		if valid {
			prefix, err := snapshot.Prefix(e.Seq)
			if err != nil {
				return err
			}
			derived, err := buildSessionTrajectory(prefix, e.SessionID, TrajectoryOptions{})
			if err != nil || !derived.Complete || derived.SourceChecksum != commit.Trajectory.SourceChecksum {
				return ErrTrajectoryIntegrity
			}
			if more, err := visit(commit, e); err != nil || !more {
				return err
			}
		}
	}
	return nil
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
			commit.Trajectory.SourceChecksum != checksumTrajectory(commit.Trajectory) ||
			!trajectorySourcesValid(events, commit.Trajectory.Sources, event.Seq) {
			return nil, session.Event{}, fmt.Errorf("%w: invalid memory commit at seq %d", ErrTrajectoryIntegrity, event.Seq)
		}
		return &commit, event, nil
	}
	return nil, session.Event{}, nil
}

func trajectorySourcesValid(events []session.Event, sources []TrajectorySource, beforeSeq int64) bool {
	if len(sources) == 0 {
		return false
	}
	for _, source := range sources {
		if source.Seq >= beforeSeq {
			return false
		}
		if _, err := resolveTrajectorySource(events, source); err != nil {
			return false
		}
	}
	return true
}
