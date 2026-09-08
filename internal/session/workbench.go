package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"code-agent/internal/identity"
)

var (
	// ErrSessionNotFound deliberately hides whether a Session is absent or is
	// owned by another actor. Transport adapters must map it to one 404 shape.
	ErrSessionNotFound = errors.New("session not found")
	// ErrSessionOwnerRequired prevents production callers from creating an
	// ownerless authorization bypass.
	ErrSessionOwnerRequired = errors.New("session owner is required")
	ErrInvalidSessionInput  = errors.New("invalid session input")
	ErrSessionStateConflict = errors.New("session state conflict")
)

const (
	sessionCreatedEventType = "session/created"
	userMessageEventType    = "user/message"
	checkpointEventType     = "checkpoint/create"
	continuationEventType   = "session/continued"
)

// EventNotifier is a coalescible hint that a Session may have a new ledger
// suffix. Consumers must read the ledger rather than treating the hint as a
// fact source.
type EventNotifier interface {
	Notify(sessionID string)
}

// WorkbenchModule is the transport seam for owner-scoped Session operations.
// Implementations must keep all writes on the canonical EventLog.
type WorkbenchModule interface {
	Create(context.Context, uint, string, string, string) (SessionView, error)
	List(context.Context, uint) ([]SessionView, error)
	Get(context.Context, uint, string) (SessionView, error)
	UpdateTitle(context.Context, uint, string, int64, string) error
	UpdateStatus(context.Context, uint, string, int64, string) error
	Delete(context.Context, uint, string, int64) error
	AppendUserMessage(context.Context, uint, string, int64, string) (EventView, error)
	Events(context.Context, uint, string, int) ([]EventView, error)
	EventsAfter(context.Context, uint, string, int64, int) ([]EventView, error)
	CreateCheckpoint(context.Context, uint, string, int64, string, string) (CheckpointView, error)
	ListCheckpoints(context.Context, uint, string) ([]CheckpointView, error)
	RestoreCheckpoint(context.Context, uint, string, string, int64) (EventView, error)
	// ContinueFromCheckpoint is retained for older transports. New production
	// callers must inject ContinuationModule so the request enters a durable run.
	ContinueFromCheckpoint(context.Context, uint, string, string, int64) (EventView, error)
}

// Workbench is the deep module used by browser transports. It keeps Session
// authorization and projections local to the canonical append-only ledger.
type Workbench struct {
	ledger EventLog
	notify EventNotifier
}

// SessionView is the current owner-scoped projection of Session facts.
type SessionView struct {
	ID          string    `json:"id"`
	UserID      uint      `json:"userId"`
	ProjectName string    `json:"projectName"`
	Title       string    `json:"title"`
	Goal        string    `json:"goal"`
	Status      string    `json:"status"`
	EventCount  int       `json:"eventCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Run         *RunView  `json:"run,omitempty"`
}

// EventView is the browser-safe representation of one canonical ledger fact.
type EventView struct {
	ID              string            `json:"id"`
	SessionID       string            `json:"sessionId"`
	Seq             int64             `json:"seq"`
	Type            string            `json:"type"`
	Author          string            `json:"author"`
	Content         string            `json:"content"`
	ToolName        string            `json:"toolName,omitempty"`
	ToolStatus      string            `json:"toolStatus,omitempty"`
	ToolOutput      string            `json:"toolOutput,omitempty"`
	Hash            string            `json:"hash"`
	PrevHash        string            `json:"prevHash"`
	CreatedAt       time.Time         `json:"createdAt"`
	RewindTargetSeq *int64            `json:"rewindTargetSeq,omitempty"`
	Continuation    *ContinuationView `json:"continuation,omitempty"`
}

// ContinuationView is the typed, browser-safe recovery receipt. Callers never
// need to parse display text to recover the selected checkpoint identity.
type ContinuationView struct {
	CheckpointHash string `json:"checkpointHash"`
	TargetEventID  string `json:"targetEventId"`
	TargetSeq      int64  `json:"targetSeq"`
	TargetHash     string `json:"targetHash"`
	ResumeCount    int    `json:"resumeCount"`
}

// CheckpointView identifies an immutable checkpoint fact and the canonical
// event it anchors. Hash is the checkpoint event checksum used for restore.
type CheckpointView struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"sessionId"`
	EventID    string    `json:"eventId"`
	Seq        int64     `json:"seq"`
	Hash       string    `json:"hash"`
	TargetHash string    `json:"targetHash"`
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"createdAt"`
}

type sessionCreatedPayload struct {
	OwnerID     uint   `json:"owner_id"`
	ProjectName string `json:"project_name"`
	Title       string `json:"title"`
	Goal        string `json:"goal,omitempty"`
	Status      string `json:"status"`
}

type messagePayload struct {
	Author  string `json:"author"`
	Content string `json:"content"`
	RunID   string `json:"run_id,omitempty"`
}

type checkpointPayload struct {
	TargetEventID  string `json:"target_event_id"`
	TargetSeq      int64  `json:"target_seq"`
	TargetChecksum string `json:"target_checksum"`
	Label          string `json:"label"`
}

// continuationPayload is the durable receipt for a Codex-style resume. The
// checkpoint anchor is copied into the event so a future reader can continue
// from one immutable fact without consulting mutable UI state.
type continuationPayload struct {
	RequestID      string         `json:"request_id,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	CheckpointHash string         `json:"checkpoint_hash"`
	TargetEventID  string         `json:"target_event_id"`
	TargetSeq      int64          `json:"target_seq"`
	TargetChecksum string         `json:"target_checksum"`
	SurfaceSHA256  string         `json:"surface_sha256,omitempty"`
	InputEventID   string         `json:"input_event_id,omitempty"`
	ResumeCount    int            `json:"resume_count"`
	Actor          identity.Actor `json:"actor,omitempty"`
}

func NewWorkbench(ledger EventLog, notify EventNotifier) *Workbench {
	return &Workbench{ledger: ledger, notify: notify}
}

// Create starts a new Session with one immutable owner-bearing fact.
func (w *Workbench) Create(ctx context.Context, ownerID uint, projectName, title, goal string) (SessionView, error) {
	if w == nil || w.ledger == nil {
		return SessionView{}, errors.New("session ledger is required")
	}
	if ownerID == 0 {
		return SessionView{}, ErrSessionOwnerRequired
	}
	projectName = strings.TrimSpace(projectName)
	title = strings.TrimSpace(title)
	if projectName == "" || title == "" {
		return SessionView{}, fmt.Errorf("%w: session project and title are required", ErrInvalidSessionInput)
	}
	sessionID, err := newEventID()
	if err != nil {
		return SessionView{}, err
	}
	event, err := w.ledger.Append(ctx, sessionID, 0, sessionCreatedEventType, sessionCreatedPayload{
		OwnerID: ownerID, ProjectName: projectName, Title: title,
		Goal: strings.TrimSpace(goal), Status: "running",
	})
	if err != nil {
		return SessionView{}, fmt.Errorf("create session: %w", err)
	}
	w.signal(sessionID)
	return SessionView{
		ID: sessionID, UserID: ownerID, ProjectName: projectName, Title: title,
		Goal: strings.TrimSpace(goal), Status: "running", EventCount: 1,
		CreatedAt: event.CreatedAt, UpdatedAt: event.CreatedAt,
	}, nil
}

// Get returns a verified projection only when ownerID owns the Session.
func (w *Workbench) Get(ctx context.Context, ownerID uint, sessionID string) (SessionView, error) {
	if ownerID == 0 || strings.TrimSpace(sessionID) == "" || w == nil || w.ledger == nil {
		return SessionView{}, ErrSessionNotFound
	}
	if err := w.ledger.Verify(ctx, sessionID); err != nil {
		return SessionView{}, err
	}
	events, err := w.ledger.Events(ctx, sessionID)
	if err != nil {
		return SessionView{}, err
	}
	view, err := reduceSessionView(events)
	if err != nil {
		return SessionView{}, err
	}
	if view.UserID != ownerID {
		return SessionView{}, ErrSessionNotFound
	}
	// A deletion is an immutable ledger fact, but the owner-facing projection
	// must behave as though the Session no longer exists. Keeping this check in
	// Get makes every mutation and read path inherit the same tombstone rule.
	if view.Status == "deleted" {
		return SessionView{}, ErrSessionNotFound
	}
	return view, nil
}

// List returns only Sessions owned by ownerID.
func (w *Workbench) List(ctx context.Context, ownerID uint) ([]SessionView, error) {
	if ownerID == 0 {
		return nil, ErrSessionOwnerRequired
	}
	if w == nil || w.ledger == nil {
		return nil, errors.New("session ledger is required")
	}
	ids, err := w.ledger.SessionIDs(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]SessionView, 0, len(ids))
	for _, id := range ids {
		view, getErr := w.Get(ctx, ownerID, id)
		if getErr != nil {
			if errors.Is(getErr, ErrSessionNotFound) {
				continue
			}
			return nil, getErr
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool {
		if !views[i].UpdatedAt.Equal(views[j].UpdatedAt) {
			return views[i].UpdatedAt.After(views[j].UpdatedAt)
		}
		return views[i].ID > views[j].ID
	})
	return views, nil
}

// UpdateTitle appends a title projection change under an explicit CAS.
func (w *Workbench) UpdateTitle(ctx context.Context, ownerID uint, sessionID string, expectedSeq int64, title string) error {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("%w: session title is required", ErrInvalidSessionInput)
	}
	if _, err := w.ledger.Append(ctx, sessionID, expectedSeq, "session/title-updated", map[string]string{"title": title}); err != nil {
		return err
	}
	w.signal(sessionID)
	return nil
}

// UpdateStatus appends a status projection change under an explicit CAS.
func (w *Workbench) UpdateStatus(ctx context.Context, ownerID uint, sessionID string, expectedSeq int64, status string) error {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return err
	}
	status = strings.TrimSpace(status)
	switch status {
	case "running", "paused", "done":
	default:
		return fmt.Errorf("%w: invalid session status", ErrInvalidSessionInput)
	}
	if _, err := w.ledger.Append(ctx, sessionID, expectedSeq, "session/status-updated", map[string]string{"status": status}); err != nil {
		return err
	}
	w.signal(sessionID)
	return nil
}

// Delete appends a tombstone and never deletes ledger facts.
func (w *Workbench) Delete(ctx context.Context, ownerID uint, sessionID string, expectedSeq int64) error {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return err
	}
	if _, err := w.ledger.Append(ctx, sessionID, expectedSeq, "session/deleted", map[string]string{"reason": "user_request"}); err != nil {
		return err
	}
	w.signal(sessionID)
	return nil
}

// AppendUserMessage appends one user-visible message under an explicit CAS.
func (w *Workbench) AppendUserMessage(ctx context.Context, ownerID uint, sessionID string, expectedSeq int64, content string) (EventView, error) {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return EventView{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return EventView{}, fmt.Errorf("%w: message content is required", ErrInvalidSessionInput)
	}
	event, err := w.ledger.AppendSurface(ctx, sessionID, expectedSeq, userMessageEventType, messagePayload{
		Author: "user", Content: content,
	}, SurfaceOperation{Op: "append"})
	if err != nil {
		return EventView{}, err
	}
	w.signal(sessionID)
	return eventToView(event)
}

// CreateCheckpoint appends an immutable recovery anchor after revalidating
// that the selected event belongs to this verified Session history.
func (w *Workbench) CreateCheckpoint(ctx context.Context, ownerID uint, sessionID string, expectedSeq int64, targetEventID, label string) (CheckpointView, error) {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return CheckpointView{}, err
	}
	targetEventID = strings.TrimSpace(targetEventID)
	label = strings.TrimSpace(label)
	if targetEventID == "" || label == "" {
		return CheckpointView{}, fmt.Errorf("%w: checkpoint target and label are required", ErrInvalidSessionInput)
	}
	events, err := w.ledger.Events(ctx, sessionID)
	if err != nil {
		return CheckpointView{}, err
	}
	surface, err := w.ledger.Surface(ctx, sessionID)
	if err != nil {
		return CheckpointView{}, err
	}
	activeTargetIDs := make(map[string]struct{}, len(surface))
	for _, event := range surface {
		activeTargetIDs[event.EventID] = struct{}{}
	}
	var target *Event
	for index := range events {
		if events[index].EventID == targetEventID {
			target = &events[index]
			break
		}
	}
	if target == nil {
		return CheckpointView{}, ErrSessionNotFound
	}
	if _, active := activeTargetIDs[target.EventID]; !active {
		return CheckpointView{}, ErrSessionNotFound
	}
	payload := checkpointPayload{
		TargetEventID: target.EventID, TargetSeq: target.Seq,
		TargetChecksum: target.Checksum, Label: label,
	}
	checkpoint, err := w.ledger.Append(ctx, sessionID, expectedSeq, checkpointEventType, payload)
	if err != nil {
		return CheckpointView{}, err
	}
	w.signal(sessionID)
	return checkpointToView(checkpoint, payload), nil
}

// RestoreCheckpoint verifies the checkpoint anchor against immutable history
// and appends exactly one rewind marker using the caller's sequence CAS.
func (w *Workbench) RestoreCheckpoint(ctx context.Context, ownerID uint, sessionID, checkpointHash string, expectedSeq int64) (EventView, error) {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return EventView{}, err
	}
	checkpointHash = strings.TrimSpace(checkpointHash)
	if checkpointHash == "" {
		return EventView{}, ErrSessionNotFound
	}
	events, err := w.ledger.Events(ctx, sessionID)
	if err != nil {
		return EventView{}, err
	}
	var checkpoint *Event
	var payload checkpointPayload
	for index := range events {
		if events[index].Type == checkpointEventType && events[index].Checksum == checkpointHash {
			checkpoint = &events[index]
			if err := json.Unmarshal(events[index].Payload, &payload); err != nil {
				return EventView{}, fmt.Errorf("%w: invalid checkpoint payload at seq %d", ErrEventIntegrity, events[index].Seq)
			}
			break
		}
	}
	if checkpoint == nil || payload.TargetSeq < 0 || payload.TargetSeq >= int64(len(events)) {
		return EventView{}, ErrSessionNotFound
	}
	target := events[payload.TargetSeq]
	if target.EventID != payload.TargetEventID || target.Checksum != payload.TargetChecksum {
		return EventView{}, fmt.Errorf("%w: checkpoint target no longer matches ledger", ErrEventIntegrity)
	}
	rewind, err := w.ledger.RewindExpected(ctx, sessionID, payload.TargetSeq, expectedSeq)
	if err != nil {
		return EventView{}, err
	}
	w.signal(sessionID)
	return eventToView(rewind)
}

// ContinueFromCheckpoint is a compatibility adapter for tests and legacy
// transports. The server wires SessionRunner for the real browser route.
func (w *Workbench) ContinueFromCheckpoint(ctx context.Context, ownerID uint, sessionID, checkpointHash string, expectedSeq int64) (EventView, error) {
	current, err := w.Get(ctx, ownerID, sessionID)
	if err != nil {
		return EventView{}, err
	}
	if current.Status != "paused" {
		return EventView{}, fmt.Errorf("%w: session must be paused before continuation", ErrSessionStateConflict)
	}
	events, err := w.ledger.Events(ctx, sessionID)
	if err != nil {
		return EventView{}, err
	}
	checkpoint, target, err := selectCheckpoint(events, checkpointHash)
	if err != nil {
		return EventView{}, err
	}
	receipt := continuationPayload{CheckpointHash: checkpoint.Checksum, TargetEventID: target.EventID, TargetSeq: target.Seq, TargetChecksum: target.Checksum, ResumeCount: 1}
	event, err := w.ledger.Append(ctx, sessionID, expectedSeq, continuationEventType, receipt)
	if err != nil {
		return EventView{}, err
	}
	w.signal(sessionID)
	return eventToView(event)
}

// Events returns a verified owner-scoped projection of the full ledger.
func (w *Workbench) Events(ctx context.Context, ownerID uint, sessionID string, limit int) ([]EventView, error) {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return nil, err
	}
	events, err := w.ledger.Events(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	views := make([]EventView, 0, len(events))
	for _, event := range events {
		view, viewErr := eventToView(event)
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, view)
	}
	return views, nil
}

// EventsAfter returns a verified suffix after an inclusive cursor.
func (w *Workbench) EventsAfter(ctx context.Context, ownerID uint, sessionID string, afterSeq int64, limit int) ([]EventView, error) {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return nil, err
	}
	events, err := w.ledger.EventsAfter(ctx, sessionID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	views := make([]EventView, 0, len(events))
	for _, event := range events {
		view, viewErr := eventToView(event)
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, view)
	}
	return views, nil
}

// ListCheckpoints derives checkpoints from immutable checkpoint/create facts.
func (w *Workbench) ListCheckpoints(ctx context.Context, ownerID uint, sessionID string) ([]CheckpointView, error) {
	if _, err := w.Get(ctx, ownerID, sessionID); err != nil {
		return nil, err
	}
	events, err := w.ledger.Events(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	checkpoints := make([]CheckpointView, 0)
	for _, event := range events {
		if event.Type != checkpointEventType {
			continue
		}
		var payload checkpointPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, fmt.Errorf("%w: invalid checkpoint payload at seq %d", ErrEventIntegrity, event.Seq)
		}
		checkpoints = append(checkpoints, checkpointToView(event, payload))
	}
	sort.SliceStable(checkpoints, func(i, j int) bool {
		return checkpoints[i].Seq > checkpoints[j].Seq
	})
	return checkpoints, nil
}

func (w *Workbench) signal(sessionID string) {
	if w.notify != nil {
		w.notify.Notify(sessionID)
	}
}

func reduceSessionView(events []Event) (SessionView, error) {
	if len(events) == 0 || events[0].Seq != 0 || events[0].Type != sessionCreatedEventType {
		return SessionView{}, ErrSessionNotFound
	}
	var created sessionCreatedPayload
	if err := json.Unmarshal(events[0].Payload, &created); err != nil || created.OwnerID == 0 {
		return SessionView{}, fmt.Errorf("%w: invalid session creation payload", ErrEventIntegrity)
	}
	view := SessionView{
		ID: events[0].SessionID, UserID: created.OwnerID,
		ProjectName: created.ProjectName, Title: created.Title, Goal: created.Goal,
		Status: created.Status, EventCount: len(events),
		CreatedAt: events[0].CreatedAt, UpdatedAt: events[len(events)-1].CreatedAt,
	}
	continuationCount := 0
	runs, runsErr := projectRuns(events)
	if runsErr != nil {
		return SessionView{}, runsErr
	}
	for _, event := range events[1:] {
		switch event.Type {
		case "session/title-updated":
			var payload struct {
				Title string `json:"title"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil || strings.TrimSpace(payload.Title) == "" {
				return SessionView{}, fmt.Errorf("%w: invalid title projection at seq %d", ErrEventIntegrity, event.Seq)
			}
			view.Title = payload.Title
		case "session/status-updated":
			var payload struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil || strings.TrimSpace(payload.Status) == "" {
				return SessionView{}, fmt.Errorf("%w: invalid status projection at seq %d", ErrEventIntegrity, event.Seq)
			}
			view.Status = payload.Status
		case "session/deleted":
			view.Status = "deleted"
		case continuationEventType:
			continuationCount++
			if err := validateContinuation(events, event, continuationCount); err != nil {
				return SessionView{}, err
			}
			var payload continuationPayload
			_ = json.Unmarshal(event.Payload, &payload)
			if payload.RunID == "" {
				view.Status = "running"
			} else {
				view.Status = "queued"
			}
		case runLeasedEventType, runHeartbeatEventType:
			view.Status = "running"
		case runCompletedEventType:
			view.Status = "done"
		case runFailedEventType:
			view.Status = "paused"
		}
	}
	var latestOrder int64 = -1
	for _, run := range runs {
		candidate := run.view
		if view.Run == nil || run.order > latestOrder {
			view.Run = &candidate
			latestOrder = run.order
		}
	}
	return view, nil
}

func eventToView(event Event) (EventView, error) {
	view := EventView{
		ID: event.EventID, SessionID: event.SessionID, Seq: event.Seq,
		Type: event.Type, Hash: event.Checksum, PrevHash: event.PrevChecksum,
		Author: "system", CreatedAt: event.CreatedAt,
	}
	switch event.Type {
	case userMessageEventType, "assistant/message":
		var payload messagePayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return EventView{}, fmt.Errorf("%w: invalid message payload at seq %d", ErrEventIntegrity, event.Seq)
		}
		view.Author, view.Content = payload.Author, payload.Content
	case "session/rewind":
		var payload rewindPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.TargetSeq < 0 {
			return EventView{}, fmt.Errorf("%w: invalid rewind payload at seq %d", ErrEventIntegrity, event.Seq)
		}
		view.RewindTargetSeq = &payload.TargetSeq
	case continuationEventType:
		payload, err := decodeContinuationPayload(event.Payload)
		if err != nil {
			return EventView{}, fmt.Errorf("%w: invalid continuation payload at seq %d", ErrEventIntegrity, event.Seq)
		}
		view.Content = fmt.Sprintf("continued from checkpoint at #%d (resume #%d)", payload.TargetSeq, payload.ResumeCount)
		view.Continuation = &ContinuationView{
			CheckpointHash: payload.CheckpointHash,
			TargetEventID:  payload.TargetEventID,
			TargetSeq:      payload.TargetSeq,
			TargetHash:     payload.TargetChecksum,
			ResumeCount:    payload.ResumeCount,
		}
	}
	return view, nil
}

func validateContinuation(events []Event, event Event, expectedResumeCount int) error {
	payload, err := decodeContinuationPayload(event.Payload)
	if err != nil || payload.ResumeCount != expectedResumeCount {
		return fmt.Errorf("%w: invalid continuation payload at seq %d", ErrEventIntegrity, event.Seq)
	}
	if payload.TargetSeq >= event.Seq || payload.TargetSeq >= int64(len(events)) {
		return fmt.Errorf("%w: invalid continuation target at seq %d", ErrEventIntegrity, event.Seq)
	}
	target := events[payload.TargetSeq]
	if target.EventID != payload.TargetEventID || target.Checksum != payload.TargetChecksum {
		return fmt.Errorf("%w: continuation target no longer matches ledger", ErrEventIntegrity)
	}
	for _, candidate := range events[:event.Seq] {
		if candidate.Type != checkpointEventType || candidate.Checksum != payload.CheckpointHash {
			continue
		}
		var checkpoint checkpointPayload
		if err := json.Unmarshal(candidate.Payload, &checkpoint); err != nil ||
			checkpoint.TargetEventID != payload.TargetEventID ||
			checkpoint.TargetSeq != payload.TargetSeq ||
			checkpoint.TargetChecksum != payload.TargetChecksum {
			return fmt.Errorf("%w: continuation checkpoint no longer matches ledger", ErrEventIntegrity)
		}
		return nil
	}
	return fmt.Errorf("%w: continuation checkpoint not found", ErrEventIntegrity)
}

func validContinuationFields(payload continuationPayload) bool {
	return strings.TrimSpace(payload.CheckpointHash) != "" &&
		strings.TrimSpace(payload.TargetEventID) != "" &&
		payload.TargetSeq >= 0 &&
		strings.TrimSpace(payload.TargetChecksum) != "" &&
		payload.ResumeCount >= 1
}

// decodeContinuationPayload accepts the pre-runner target_hash spelling for
// immutable ledger history while normalizing all callers onto target_checksum.
// The alias does not weaken validation: the referenced checkpoint and target
// event are still verified against the canonical hash chain.
func decodeContinuationPayload(raw json.RawMessage) (continuationPayload, error) {
	var persisted struct {
		continuationPayload
		TargetHash string `json:"target_hash"`
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return continuationPayload{}, err
	}
	payload := persisted.continuationPayload
	if strings.TrimSpace(payload.TargetChecksum) == "" {
		payload.TargetChecksum = strings.TrimSpace(persisted.TargetHash)
	}
	if !validContinuationFields(payload) {
		return continuationPayload{}, ErrEventIntegrity
	}
	return payload, nil
}

func checkpointToView(event Event, payload checkpointPayload) CheckpointView {
	return CheckpointView{
		ID: event.EventID, SessionID: event.SessionID,
		EventID: payload.TargetEventID, Seq: payload.TargetSeq,
		Hash: event.Checksum, TargetHash: payload.TargetChecksum,
		Label: payload.Label, CreatedAt: event.CreatedAt,
	}
}
