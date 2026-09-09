package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
)

const (
	runLeasedEventType    = "session/run-leased"
	runHeartbeatEventType = "session/run-heartbeat"
	runCompletedEventType = "session/run-completed"
	runFailedEventType    = "session/run-failed"
	toolDispatchedType    = "tool/dispatched"
	toolUnknownEventType  = "tool/unknown"
)

var (
	ErrRunNotFound = errors.New("session run not found")
	ErrLeaseLost   = errors.New("session run lease lost")
)

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
)

// ContinueCommand is the complete caller intent accepted by the SessionRunner
// interface. RequestID is the idempotency key; ExpectedSeq remains the ledger
// CAS for a new request.
type ContinueCommand struct {
	RequestID      string
	SessionID      string
	CheckpointHash string
	OwnerID        uint
	ExpectedSeq    int64
	Actor          identity.Actor
}

type RunView struct {
	SessionID      string     `json:"sessionId"`
	RunID          string     `json:"runId"`
	RequestID      string     `json:"requestId"`
	Status         RunStatus  `json:"status"`
	Attempt        int        `json:"attempt"`
	WorkerID       string     `json:"workerId,omitempty"`
	CheckpointHash string     `json:"checkpointHash"`
	LeaseUntil     *time.Time `json:"leaseUntil,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	Error          string     `json:"error,omitempty"`
	RetryOfRunID   string     `json:"retryOfRunId,omitempty"`
	RetryOfRunIDs  []string   `json:"retryOfRunIds,omitempty"`
}

// ContinuationModule is the small transport-facing interface of the deep
// SessionRunner module.
type ContinuationModule interface {
	RequestContinuation(context.Context, ContinueCommand) (RunView, error)
	Recover(context.Context) error
	Close() error
}

type ConversationAdapter interface {
	RunConversation(context.Context, orchestrator.ConversationRequest, orchestrator.ConversationHandlers) (orchestrator.ConversationResult, error)
}

type ToolExecutionAdapter interface {
	Execute(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult
}

// ToolExecutionFunc adapts the Harness-owned tool executor without exposing
// executor internals to the continuation module.
type ToolExecutionFunc func(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult

func (f ToolExecutionFunc) Execute(ctx context.Context, actor identity.Actor, sessionID string, call orchestrator.ToolCall) orchestrator.ToolResult {
	return f(ctx, actor, sessionID, call)
}

type SessionRunnerOptions struct {
	WorkerID          string
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	QueueSize         int
	Now               func() time.Time
}

type SessionRunner struct {
	workbench    *Workbench
	conversation ConversationAdapter
	tools        ToolExecutionAdapter
	options      SessionRunnerOptions
	ctx          context.Context
	cancel       context.CancelFunc
	queue        chan runKey
	queuedMu     sync.Mutex
	queued       map[runKey]struct{}
	wg           sync.WaitGroup
}

type runKey struct {
	sessionID string
	runID     string
}

type runLeasePayload struct {
	RunID      string    `json:"run_id"`
	RequestID  string    `json:"request_id"`
	LeaseID    string    `json:"lease_id"`
	WorkerID   string    `json:"worker_id"`
	Attempt    int       `json:"attempt"`
	LeaseUntil time.Time `json:"lease_until"`
}

type runTerminalPayload struct {
	RunID     string `json:"run_id"`
	RequestID string `json:"request_id"`
	LeaseID   string `json:"lease_id"`
	Attempt   int    `json:"attempt"`
	Error     string `json:"error,omitempty"`
}

type toolCallPayload struct {
	RunID         string `json:"run_id"`
	ToolCallID    string `json:"tool_call_id"`
	ToolName      string `json:"tool_name"`
	ArgumentsJSON string `json:"arguments_json"`
}

type toolResultPayload struct {
	RunID      string `json:"run_id"`
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	ExitCode   int32  `json:"exit_code"`
	Truncated  bool   `json:"truncated,omitempty"`
}

type runProjection struct {
	view         RunView
	leaseID      string
	inputEvent   string
	surfaceHash  string
	targetSeq    int64
	actor        identity.Actor
	terminal     bool
	order        int64
	createdOrder int64
}

func NewSessionRunner(workbench *Workbench, conversation ConversationAdapter, tools ToolExecutionAdapter, options SessionRunnerOptions) *SessionRunner {
	if strings.TrimSpace(options.WorkerID) == "" {
		if id, err := newEventID(); err == nil {
			options.WorkerID = "worker:" + id
		} else {
			options.WorkerID = "worker:local"
		}
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = 30 * time.Second
	}
	if options.HeartbeatInterval <= 0 || options.HeartbeatInterval >= options.LeaseDuration {
		options.HeartbeatInterval = options.LeaseDuration / 3
	}
	if options.QueueSize <= 0 {
		options.QueueSize = 256
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := &SessionRunner{
		workbench: workbench, conversation: conversation, tools: tools, options: options,
		ctx: ctx, cancel: cancel, queue: make(chan runKey, options.QueueSize), queued: make(map[runKey]struct{}),
	}
	runner.wg.Add(1)
	go runner.workerLoop()
	return runner
}

func (r *SessionRunner) RequestContinuation(ctx context.Context, command ContinueCommand) (RunView, error) {
	if r == nil || r.workbench == nil || r.workbench.ledger == nil {
		return RunView{}, errors.New("session runner requires a workbench")
	}
	if r.conversation == nil {
		return RunView{}, errors.New("session runner requires an orchestrator")
	}
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.CheckpointHash = strings.TrimSpace(command.CheckpointHash)
	if command.RequestID == "" || command.SessionID == "" || command.ExpectedSeq < 0 {
		return RunView{}, fmt.Errorf("%w: requestId, sessionId, and expectedSeq are required", ErrInvalidSessionInput)
	}
	if _, err := r.workbench.Get(ctx, command.OwnerID, command.SessionID); err != nil {
		return RunView{}, err
	}
	actor, err := command.Actor.BindSession(command.SessionID)
	if err != nil {
		return RunView{}, fmt.Errorf("%w: invalid continuation actor", ErrInvalidSessionInput)
	}
	events, err := r.workbench.ledger.Events(ctx, command.SessionID)
	if err != nil {
		return RunView{}, err
	}
	if existing, ok, projectErr := runForRequest(events, command.RequestID); projectErr != nil {
		return RunView{}, projectErr
	} else if ok {
		if (command.CheckpointHash != "" && existing.view.CheckpointHash != command.CheckpointHash) || existing.actor.ScopeKey() != actor.ScopeKey() {
			return RunView{}, fmt.Errorf("%w: requestId is already bound to another continuation", ErrSessionStateConflict)
		}
		r.enqueue(runKey{sessionID: command.SessionID, runID: existing.view.RunID}, time.Time{})
		return existing.view, nil
	}
	current, err := reduceSessionView(events)
	if err != nil {
		return RunView{}, err
	}
	if current.Status != "paused" {
		return RunView{}, fmt.Errorf("%w: session must be paused before continuation", ErrSessionStateConflict)
	}
	runID := runIDForRequest(command.SessionID, command.RequestID)
	payload, err := r.workbench.continuationRequestPayload(ctx, command.SessionID, command.CheckpointHash, command.RequestID, runID, actor)
	if err != nil {
		return RunView{}, err
	}
	_, err = r.workbench.ledger.Append(ctx, command.SessionID, command.ExpectedSeq, continuationEventType, payload)
	if err != nil {
		if errors.Is(err, ErrSequenceConflict) {
			latest, readErr := r.workbench.ledger.Events(ctx, command.SessionID)
			if readErr == nil {
				if existing, ok, projectErr := runForRequest(latest, command.RequestID); projectErr != nil {
					return RunView{}, projectErr
				} else if ok {
					if (command.CheckpointHash != "" && existing.view.CheckpointHash != command.CheckpointHash) || existing.actor.ScopeKey() != actor.ScopeKey() {
						return RunView{}, fmt.Errorf("%w: requestId is already bound to another continuation", ErrSessionStateConflict)
					}
					r.enqueue(runKey{sessionID: command.SessionID, runID: existing.view.RunID}, time.Time{})
					return existing.view, nil
				}
			}
		}
		return RunView{}, err
	}
	r.workbench.signal(command.SessionID)
	view := RunView{
		SessionID: command.SessionID, RunID: runID, RequestID: command.RequestID,
		Status: RunQueued, CheckpointHash: payload.CheckpointHash,
	}
	r.enqueue(runKey{sessionID: command.SessionID, runID: runID}, time.Time{})
	return view, nil
}

func runIDForRequest(sessionID, requestID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(sessionID) + "\x00" + strings.TrimSpace(requestID)))
	return "run:" + hex.EncodeToString(digest[:16])
}

func (w *Workbench) continuationRequestPayload(ctx context.Context, sessionID, checkpointHash, requestID, runID string, actor identity.Actor) (continuationPayload, error) {
	events, err := w.ledger.Events(ctx, sessionID)
	if err != nil {
		return continuationPayload{}, err
	}
	checkpoint, anchor, err := selectCheckpoint(events, checkpointHash)
	if err != nil {
		return continuationPayload{}, err
	}
	surface, err := surfaceAt(events, anchor.Seq)
	if err != nil {
		return continuationPayload{}, err
	}
	inputEventID := ""
	if len(surface) > 0 && surface[len(surface)-1].Type == userMessageEventType {
		inputEventID = surface[len(surface)-1].EventID
	}
	resumeCount := 1
	for _, event := range events {
		if event.Type == continuationEventType {
			resumeCount++
		}
	}
	return continuationPayload{
		RequestID: requestID, RunID: runID, CheckpointHash: checkpoint.Checksum,
		TargetEventID: anchor.EventID, TargetSeq: anchor.Seq, TargetChecksum: anchor.Checksum,
		SurfaceSHA256: checksumSurface(surface), InputEventID: inputEventID,
		ResumeCount: resumeCount, Actor: actor,
	}, nil
}

func selectCheckpoint(events []Event, checkpointHash string) (Event, Event, error) {
	checkpointHash = strings.TrimSpace(checkpointHash)
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type != checkpointEventType || (checkpointHash != "" && events[index].Checksum != checkpointHash) {
			continue
		}
		var payload checkpointPayload
		if err := json.Unmarshal(events[index].Payload, &payload); err != nil || payload.TargetSeq < 0 || payload.TargetSeq >= int64(index) {
			return Event{}, Event{}, fmt.Errorf("%w: invalid checkpoint payload at seq %d", ErrEventIntegrity, events[index].Seq)
		}
		anchor := events[payload.TargetSeq]
		if anchor.EventID != payload.TargetEventID || anchor.Checksum != payload.TargetChecksum {
			return Event{}, Event{}, fmt.Errorf("%w: checkpoint target no longer matches ledger", ErrEventIntegrity)
		}
		return events[index], anchor, nil
	}
	return Event{}, Event{}, ErrSessionNotFound
}

func surfaceAt(events []Event, targetSeq int64) ([]Event, error) {
	if targetSeq < 0 || targetSeq >= int64(len(events)) {
		return nil, ErrSessionNotFound
	}
	return projectSurface(events[:targetSeq+1])
}

func checksumSurface(surface []Event) string {
	hash := sha256.New()
	for _, event := range surface {
		_, _ = hash.Write([]byte(event.Checksum))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (r *SessionRunner) Recover(ctx context.Context) error {
	if r == nil || r.workbench == nil || r.workbench.ledger == nil {
		return errors.New("session runner requires a workbench")
	}
	ids, err := r.workbench.ledger.SessionIDs(ctx)
	if err != nil {
		return err
	}
	for _, sessionID := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		events, readErr := r.workbench.ledger.Events(ctx, sessionID)
		if readErr != nil {
			return readErr
		}
		runs, projectErr := projectRuns(events)
		if projectErr != nil {
			return projectErr
		}
		for runID, run := range runs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if run.terminal {
				continue
			}
			wakeAt := time.Time{}
			if run.view.LeaseUntil != nil && run.view.LeaseUntil.After(r.now()) {
				wakeAt = *run.view.LeaseUntil
			}
			r.enqueue(runKey{sessionID: sessionID, runID: runID}, wakeAt)
		}
	}
	return nil
}

func (r *SessionRunner) Close() error {
	if r == nil || r.cancel == nil {
		return nil
	}
	r.cancel()
	r.wg.Wait()
	r.queuedMu.Lock()
	clear(r.queued)
	for {
		select {
		case <-r.queue:
		default:
			r.queuedMu.Unlock()
			return nil
		}
	}
}

func (r *SessionRunner) now() time.Time { return r.options.Now().UTC() }

func (r *SessionRunner) enqueue(key runKey, wakeAt time.Time) {
	r.queuedMu.Lock()
	if _, exists := r.queued[key]; exists {
		r.queuedMu.Unlock()
		return
	}
	r.queued[key] = struct{}{}
	r.queuedMu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if !wakeAt.IsZero() {
			timer := time.NewTimer(time.Until(wakeAt))
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.ctx.Done():
				r.releaseQueued(key)
				return
			}
		}
		select {
		case r.queue <- key:
		case <-r.ctx.Done():
			r.releaseQueued(key)
		}
	}()
}

func (r *SessionRunner) releaseQueued(key runKey) {
	r.queuedMu.Lock()
	delete(r.queued, key)
	r.queuedMu.Unlock()
}

func (r *SessionRunner) workerLoop() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case key := <-r.queue:
			r.releaseQueued(key)
			r.execute(key)
		}
	}
}

func (r *SessionRunner) execute(key runKey) {
	lease, waitUntil, err := r.claim(r.ctx, key)
	if err != nil {
		return
	}
	if !waitUntil.IsZero() {
		r.enqueue(key, waitUntil)
		return
	}
	if committed, committedErr := r.hasCommittedAssistant(r.ctx, key); committedErr != nil {
		r.fail(key, lease, committedErr)
		return
	} else if committed {
		_ = r.appendTerminal(context.Background(), key, lease, runCompletedEventType, "")
		return
	}
	runCtx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	heartbeatDone := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go r.heartbeat(runCtx, key, lease, heartbeatDone, cancel, heartbeatErr)

	request, err := r.conversationRequest(runCtx, key, lease)
	if err != nil {
		cancel()
		<-heartbeatDone
		r.fail(key, lease, err)
		return
	}
	// A recovered tool_after/model_after checkpoint can legitimately have no
	// new user text: the canonical Surface history already contains the
	// assistant tool call and its committed result. Python's runner uses the
	// request-scoped resume flag to continue that history-only turn. Only an
	// entirely empty request is invalid, and that is rejected before enqueue.
	if strings.TrimSpace(request.Input) == "" && len(request.History) == 0 {
		cancel()
		<-heartbeatDone
		r.fail(key, lease, errors.New("continuation has no input or history"))
		return
	}
	var blocked atomic.Bool
	retryRunIDs := normalizedRetryRunIDs(request.RetryOfRunID, request.RetryOfRunIDs)
	handlers := orchestrator.ConversationHandlers{
		Tool: func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
			result, unsafe := r.executeTool(ctx, key, lease, request.Actor, call, retryRunIDs...)
			if unsafe {
				blocked.Store(true)
			}
			return result
		},
	}
	result, runErr := r.conversation.RunConversation(runCtx, request, handlers)
	cancel()
	<-heartbeatDone
	select {
	case heartbeatFailure := <-heartbeatErr:
		if runErr == nil {
			runErr = fmt.Errorf("session run heartbeat failed: %w", heartbeatFailure)
		}
	default:
	}
	// A graceful server shutdown is not a terminal Agent outcome. Leave the
	// durable lease non-terminal so the next process can claim it after expiry
	// and resume from the Python checkpoint instead of projecting a false
	// permanent failure.
	if r.ctx.Err() != nil && (runErr == nil || errors.Is(runErr, context.Canceled)) {
		return
	}
	// A broken gRPC transport is not a terminal Agent outcome. Keep the
	// current lease as the fencing record and retry the same durable run only
	// after it expires; Python can then resume its run-bound checkpoint.
	if runErr != nil && orchestrator.IsConnectionError(runErr) {
		if retryErr := r.requeueAfterLease(key, lease); retryErr == nil {
			return
		}
	}
	if runErr != nil || !result.Success || blocked.Load() {
		if runErr == nil {
			runErr = errors.New("orchestrator returned an unsuccessful terminal state")
		}
		r.fail(key, lease, runErr)
		return
	}
	message := strings.TrimSpace(result.Message)
	if message == "" {
		r.fail(key, lease, errors.New("orchestrator returned an empty response"))
		return
	}
	if err := r.appendLeasedSurface(context.Background(), key, lease, "assistant/message", messagePayload{
		Author: "assistant", Content: message, RunID: key.runID,
	}); err != nil {
		r.fail(key, lease, err)
		return
	}
	_ = r.appendTerminal(context.Background(), key, lease, runCompletedEventType, "")
}

func (r *SessionRunner) requeueAfterLease(key runKey, lease runLeasePayload) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := r.workbench.ledger.Events(ctx, key.sessionID)
	if err != nil {
		return err
	}
	projection, err := projectRun(events, key.runID)
	if err != nil {
		return err
	}
	if projection.terminal || projection.leaseID != lease.LeaseID {
		return ErrLeaseLost
	}
	wakeAt := r.now()
	if projection.view.LeaseUntil != nil && projection.view.LeaseUntil.After(wakeAt) {
		wakeAt = *projection.view.LeaseUntil
	}
	r.enqueue(key, wakeAt)
	return nil
}

func (r *SessionRunner) hasCommittedAssistant(ctx context.Context, key runKey) (bool, error) {
	events, err := r.workbench.ledger.Events(ctx, key.sessionID)
	if err != nil {
		return false, err
	}
	for _, event := range events {
		if event.Type != "assistant/message" {
			continue
		}
		var payload messagePayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false, fmt.Errorf("%w: invalid assistant message at seq %d", ErrEventIntegrity, event.Seq)
		}
		if payload.RunID == key.runID {
			return true, nil
		}
	}
	return false, nil
}

func (r *SessionRunner) claim(ctx context.Context, key runKey) (runLeasePayload, time.Time, error) {
	for attempt := 0; attempt < 16; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return runLeasePayload{}, time.Time{}, err
		}
		projection, err := projectRun(events, key.runID)
		if err != nil {
			return runLeasePayload{}, time.Time{}, err
		}
		if projection.terminal {
			return runLeasePayload{}, time.Time{}, ErrLeaseLost
		}
		if projection.view.LeaseUntil != nil && projection.view.LeaseUntil.After(r.now()) {
			return runLeasePayload{}, *projection.view.LeaseUntil, nil
		}
		leaseID, idErr := newEventID()
		if idErr != nil {
			return runLeasePayload{}, time.Time{}, idErr
		}
		lease := runLeasePayload{
			RunID: key.runID, RequestID: projection.view.RequestID, LeaseID: leaseID,
			WorkerID: r.options.WorkerID, Attempt: projection.view.Attempt + 1,
			LeaseUntil: r.now().Add(r.options.LeaseDuration),
		}
		if _, err = r.workbench.ledger.Append(ctx, key.sessionID, int64(len(events)), runLeasedEventType, lease); err == nil {
			r.workbench.signal(key.sessionID)
			return lease, time.Time{}, nil
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return runLeasePayload{}, time.Time{}, err
		}
	}
	return runLeasePayload{}, time.Time{}, ErrSequenceConflict
}

func (r *SessionRunner) heartbeat(ctx context.Context, key runKey, lease runLeasePayload, done chan<- struct{}, cancel context.CancelFunc, failure chan<- error) {
	defer close(done)
	ticker := time.NewTicker(r.options.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lease.LeaseUntil = r.now().Add(r.options.LeaseDuration)
			if err := r.appendLeasedFact(ctx, key, lease.LeaseID, runHeartbeatEventType, lease); err != nil {
				select {
				case failure <- err:
				default:
				}
				cancel()
				return
			}
		}
	}
}

func (r *SessionRunner) conversationRequest(ctx context.Context, key runKey, lease runLeasePayload) (orchestrator.ConversationRequest, error) {
	events, err := r.workbench.ledger.Events(ctx, key.sessionID)
	if err != nil {
		return orchestrator.ConversationRequest{}, err
	}
	projection, err := projectRun(events, key.runID)
	if err != nil {
		return orchestrator.ConversationRequest{}, err
	}
	if projection.leaseID != lease.LeaseID {
		return orchestrator.ConversationRequest{}, ErrLeaseLost
	}
	surface, err := r.workbench.ledger.Surface(ctx, key.sessionID)
	if err != nil {
		return orchestrator.ConversationRequest{}, err
	}
	requestRetryOfRunID := ""
	retryOfRunIDs := []string(nil)
	if projection.surfaceHash != "" {
		checkpointSurface, surfaceErr := surfaceAt(events, projection.targetSeq)
		if surfaceErr != nil || projection.surfaceHash != checksumSurface(checkpointSurface) {
			return orchestrator.ConversationRequest{}, fmt.Errorf("%w: continuation surface has changed", ErrEventIntegrity)
		}
		retryOfRunIDs = failedRetryRunIDs(events, key.runID, projection.view.CheckpointHash, projection.actor.ScopeKey())
		if err := validateContinuationSurface(checkpointSurface, surface, key.runID, retryOfRunIDs...); err != nil {
			return orchestrator.ConversationRequest{}, err
		}
		if len(retryOfRunIDs) > 0 {
			requestRetryOfRunID = retryOfRunIDs[0]
		}
	}
	history, input, err := conversationHistory(surface, events, key.runID, projection.inputEvent)
	if err != nil {
		return orchestrator.ConversationRequest{}, err
	}
	return orchestrator.ConversationRequest{
		Input: input, SessionID: key.sessionID, RunID: key.runID, Resume: true, SurfaceSHA256: projection.surfaceHash,
		RetryOfRunID: requestRetryOfRunID, RetryOfRunIDs: retryOfRunIDs,
		Actor: projection.actor, History: history,
	}, nil
}

func validateContinuationSurface(checkpoint, current []Event, runID string, retryOfRunIDs ...string) error {
	if len(current) < len(checkpoint) {
		return fmt.Errorf("%w: continuation surface prefix is missing", ErrEventIntegrity)
	}
	for index := range checkpoint {
		if current[index].Checksum != checkpoint[index].Checksum {
			return fmt.Errorf("%w: continuation surface prefix has changed", ErrEventIntegrity)
		}
	}
	allowedRunIDs := map[string]struct{}{runID: {}}
	for _, retryOfRunID := range retryOfRunIDs {
		if strings.TrimSpace(retryOfRunID) != "" {
			allowedRunIDs[strings.TrimSpace(retryOfRunID)] = struct{}{}
		}
	}
	for _, event := range current[len(checkpoint):] {
		switch event.Type {
		case "tool/call":
			var payload toolCallPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				return fmt.Errorf("%w: continuation surface contains a foreign tool call", ErrEventIntegrity)
			}
			if _, ok := allowedRunIDs[payload.RunID]; !ok {
				return fmt.Errorf("%w: continuation surface contains a foreign tool call", ErrEventIntegrity)
			}
		case "tool/result":
			var payload toolResultPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				return fmt.Errorf("%w: continuation surface contains a foreign tool result", ErrEventIntegrity)
			}
			if _, ok := allowedRunIDs[payload.RunID]; !ok {
				return fmt.Errorf("%w: continuation surface contains a foreign tool result", ErrEventIntegrity)
			}
		case "assistant/message":
			var payload messagePayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				return fmt.Errorf("%w: continuation surface contains a foreign assistant message", ErrEventIntegrity)
			}
			if _, ok := allowedRunIDs[payload.RunID]; !ok {
				return fmt.Errorf("%w: continuation surface contains a foreign assistant message", ErrEventIntegrity)
			}
		default:
			return fmt.Errorf("%w: continuation surface contains an unexpected %s event", ErrEventIntegrity, event.Type)
		}
	}
	return nil
}

func conversationHistory(surface, events []Event, runID, inputEventID string) ([]orchestrator.ConversationMessage, string, error) {
	results := make(map[string]toolResultPayload)
	dispatched := make(map[string]bool)
	for _, event := range events {
		switch event.Type {
		case toolDispatchedType:
			var payload toolCallPayload
			if json.Unmarshal(event.Payload, &payload) == nil {
				dispatched[invocationHistoryKey(payload.RunID, payload.ToolCallID)] = true
			}
		case "tool/result":
			var payload toolResultPayload
			if json.Unmarshal(event.Payload, &payload) == nil {
				results[invocationHistoryKey(payload.RunID, payload.ToolCallID)] = payload
			}
		}
	}
	history := make([]orchestrator.ConversationMessage, 0, len(surface))
	input := ""
	for _, event := range surface {
		switch event.Type {
		case userMessageEventType, "assistant/message":
			var payload messagePayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, "", fmt.Errorf("%w: invalid message payload at seq %d", ErrEventIntegrity, event.Seq)
			}
			if event.EventID == inputEventID {
				input = payload.Content
				continue
			}
			history = append(history, orchestrator.ConversationMessage{
				Role: payload.Author, Content: payload.Content, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), SchemaVersion: 1,
			})
		case "tool/call":
			var payload toolCallPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, "", fmt.Errorf("%w: invalid tool call at seq %d", ErrEventIntegrity, event.Seq)
			}
			key := invocationHistoryKey(payload.RunID, payload.ToolCallID)
			if _, complete := results[key]; !complete {
				if dispatched[key] {
					return nil, "", fmt.Errorf("tool invocation %s requires reconciliation", payload.ToolCallID)
				}
				continue
			}
			history = append(history, orchestrator.ConversationMessage{
				Role: "assistant", CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), SchemaVersion: 1,
				ToolCalls: []orchestrator.ConversationToolCall{{ID: payload.ToolCallID, Name: payload.ToolName, ArgumentsJSON: payload.ArgumentsJSON}},
			})
		case "tool/result":
			var payload toolResultPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, "", fmt.Errorf("%w: invalid tool result at seq %d", ErrEventIntegrity, event.Seq)
			}
			history = append(history, orchestrator.ConversationMessage{
				Role: "tool", Content: payload.Output, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), SchemaVersion: 1,
				Name: payload.ToolName, ToolCallID: payload.ToolCallID, IsError: payload.Error != "",
			})
		}
	}
	return history, input, nil
}

func invocationHistoryKey(runID, callID string) string {
	return strings.TrimSpace(runID) + "\x00" + strings.TrimSpace(callID)
}

func (r *SessionRunner) executeTool(ctx context.Context, key runKey, lease runLeasePayload, actor identity.Actor, call orchestrator.ToolCall, retryOfRunIDs ...string) (orchestrator.ToolResult, bool) {
	call.ID = strings.TrimSpace(call.ID)
	call.Name = strings.TrimSpace(call.Name)
	if call.ID == "" || call.Name == "" {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "invalid tool request", ExitCode: 1}, true
	}
	canonicalArguments, canonicalErr := canonicalToolArguments(call.ParametersJSON)
	if canonicalErr != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "invalid tool parameters", ExitCode: 1}, true
	}
	call.ParametersJSON = canonicalArguments
	events, err := r.workbench.ledger.Events(ctx, key.sessionID)
	if err != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool ledger unavailable", ExitCode: 1}, true
	}
	state := invocationProjection(events, key.runID, call.ID)
	if (state.prepared != nil && !toolCallMatches(*state.prepared, call)) ||
		(state.dispatched != nil && !toolCallMatches(*state.dispatched, call)) ||
		(state.result != nil && (state.prepared == nil || state.result.ToolName != call.Name)) {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool invocation identity conflict", ExitCode: 1}, true
	}
	if state.result != nil {
		return toolResultFromPayload(*state.result), false
	}
	// A retry may replay a tool request whose result was durably committed by
	// the failed predecessor. Reuse that receipt and materialize a current-run
	// call/result pair for auditability; never invoke the external tool again.
	retryOfRunIDs = normalizedRetryRunIDs("", retryOfRunIDs)
	if len(retryOfRunIDs) > 0 {
		unknownPrior := false
		for _, retryOfRunID := range retryOfRunIDs {
			prior := invocationProjection(events, retryOfRunID, call.ID)
			if (prior.prepared != nil && !toolCallMatches(*prior.prepared, call)) ||
				(prior.dispatched != nil && !toolCallMatches(*prior.dispatched, call)) {
				return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool invocation identity conflict", ExitCode: 1}, true
			}
			if prior.result != nil {
				if prior.prepared == nil {
					return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool result has no prepared invocation", ExitCode: 1}, true
				}
				callPayload := toolCallPayload{RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON}
				if state.prepared == nil {
					if err := r.appendLeasedSurface(ctx, key, lease, "tool/call", callPayload); err != nil {
						return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool preparation persistence failed", ExitCode: 1}, true
					}
				}
				reused := toolResultFromPayload(*prior.result)
				if err := r.appendLeasedSurface(ctx, key, lease, "tool/result", toolResultPayloadFrom(reused, key.runID)); err != nil {
					return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool result persistence failed", ExitCode: 1}, true
				}
				return reused, false
			}
			if prior.dispatched != nil {
				unknownPrior = true
			}
		}
		if unknownPrior {
			_ = r.appendLeasedFact(ctx, key, lease.LeaseID, toolUnknownEventType, toolCallPayload{RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON})
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool invocation requires reconciliation", ExitCode: 1}, true
		}
	}
	if state.dispatched != nil {
		_ = r.appendLeasedFact(ctx, key, lease.LeaseID, toolUnknownEventType, toolCallPayload{RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON})
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool invocation requires reconciliation", ExitCode: 1}, true
	}
	callPayload := toolCallPayload{RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON}
	if state.prepared == nil {
		if err := r.appendLeasedSurface(ctx, key, lease, "tool/call", callPayload); err != nil {
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool preparation persistence failed", ExitCode: 1}, true
		}
	}
	if err := r.appendLeasedFact(ctx, key, lease.LeaseID, toolDispatchedType, callPayload); err != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool dispatch persistence failed", ExitCode: 1}, true
	}
	if r.tools == nil {
		result := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool execution is unavailable", ExitCode: 1}
		_ = r.appendLeasedSurface(ctx, key, lease, "tool/result", toolResultPayloadFrom(result, key.runID))
		return result, false
	}
	result := r.tools.Execute(ctx, actor, key.sessionID, call)
	result.ToolCallID = call.ID
	result.ToolName = call.Name
	if err := r.appendLeasedSurface(ctx, key, lease, "tool/result", toolResultPayloadFrom(result, key.runID)); err != nil {
		_ = r.appendLeasedFact(context.Background(), key, lease.LeaseID, toolUnknownEventType, callPayload)
		result.Error = "tool result persistence failed"
		result.ExitCode = 1
		return result, true
	}
	return result, false
}

func canonicalToolArguments(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}", nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func toolCallMatches(payload toolCallPayload, call orchestrator.ToolCall) bool {
	arguments, err := canonicalToolArguments(payload.ArgumentsJSON)
	return err == nil && strings.TrimSpace(payload.ToolName) == call.Name && arguments == call.ParametersJSON
}

type invocationView struct {
	prepared   *toolCallPayload
	dispatched *toolCallPayload
	result     *toolResultPayload
}

func invocationProjection(events []Event, runID, callID string) invocationView {
	var out invocationView
	for _, event := range events {
		switch event.Type {
		case "tool/call", toolDispatchedType:
			var payload toolCallPayload
			if json.Unmarshal(event.Payload, &payload) != nil || payload.RunID != runID || payload.ToolCallID != callID {
				continue
			}
			if event.Type == "tool/call" {
				copy := payload
				out.prepared = &copy
			} else {
				copy := payload
				out.dispatched = &copy
			}
		case "tool/result":
			var payload toolResultPayload
			if json.Unmarshal(event.Payload, &payload) == nil && payload.RunID == runID && payload.ToolCallID == callID {
				copy := payload
				out.result = &copy
			}
		}
	}
	return out
}

func toolResultPayloadFrom(result orchestrator.ToolResult, runID string) toolResultPayload {
	return toolResultPayload{RunID: runID, ToolCallID: result.ToolCallID, ToolName: result.ToolName, Output: result.Output, Error: result.Error, ExitCode: result.ExitCode, Truncated: result.Truncated}
}

func toolResultFromPayload(payload toolResultPayload) orchestrator.ToolResult {
	return orchestrator.ToolResult{ToolCallID: payload.ToolCallID, ToolName: payload.ToolName, Output: payload.Output, Error: payload.Error, ExitCode: payload.ExitCode, Truncated: payload.Truncated}
}

func (r *SessionRunner) appendLeasedSurface(ctx context.Context, key runKey, lease runLeasePayload, eventType string, payload any) error {
	for attempt := 0; attempt < 16; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return err
		}
		projection, err := projectRun(events, key.runID)
		if err != nil {
			return err
		}
		if projection.terminal || projection.leaseID != lease.LeaseID {
			return ErrLeaseLost
		}
		_, err = r.workbench.ledger.AppendSurface(ctx, key.sessionID, int64(len(events)), eventType, payload, SurfaceOperation{Op: "append"})
		if err == nil {
			r.workbench.signal(key.sessionID)
			return nil
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return err
		}
	}
	return ErrSequenceConflict
}

func (r *SessionRunner) appendLeasedFact(ctx context.Context, key runKey, leaseID, eventType string, payload any) error {
	for attempt := 0; attempt < 16; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return err
		}
		projection, err := projectRun(events, key.runID)
		if err != nil {
			return err
		}
		if projection.terminal || projection.leaseID != leaseID {
			return ErrLeaseLost
		}
		_, err = r.workbench.ledger.Append(ctx, key.sessionID, int64(len(events)), eventType, payload)
		if err == nil {
			r.workbench.signal(key.sessionID)
			return nil
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return err
		}
	}
	return ErrSequenceConflict
}

func (r *SessionRunner) appendTerminal(ctx context.Context, key runKey, lease runLeasePayload, eventType, publicError string) error {
	payload := runTerminalPayload{RunID: key.runID, RequestID: lease.RequestID, LeaseID: lease.LeaseID, Attempt: lease.Attempt, Error: publicError}
	return r.appendLeasedFact(ctx, key, lease.LeaseID, eventType, payload)
}

func (r *SessionRunner) fail(key runKey, lease runLeasePayload, _ error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.appendTerminal(ctx, key, lease, runFailedEventType, "agent continuation failed")
}

func (r *SessionRunner) runView(ctx context.Context, sessionID, runID string) (RunView, error) {
	events, err := r.workbench.ledger.Events(ctx, sessionID)
	if err != nil {
		return RunView{}, err
	}
	projection, err := projectRun(events, runID)
	return projection.view, err
}

// Run returns one durable run projection. It is primarily the polling and test
// surface; browser transports normally observe the same facts over WebSocket.
func (r *SessionRunner) Run(ctx context.Context, sessionID, runID string) (RunView, error) {
	if r == nil || r.workbench == nil || r.workbench.ledger == nil {
		return RunView{}, ErrRunNotFound
	}
	return r.runView(ctx, strings.TrimSpace(sessionID), strings.TrimSpace(runID))
}

func runForRequest(events []Event, requestID string) (runProjection, bool, error) {
	runs, err := projectRuns(events)
	if err != nil {
		return runProjection{}, false, err
	}
	for _, run := range runs {
		if run.view.RequestID == requestID {
			return run, true, nil
		}
	}
	return runProjection{}, false, nil
}

// failedRetryRunIDs returns terminal failures for the same checkpoint in
// lineage order. The oldest failure is the stable retry root: Python may have
// persisted a checkpoint under any later attempt, or may still have the root
// checkpoint after an attempt failed before its first checkpoint write.
// Unrelated unfinished runs remain isolated.
func failedRetryRunIDs(events []Event, currentRunID, checkpointHash string, actorScopes ...string) []string {
	checkpointHash = strings.TrimSpace(checkpointHash)
	if checkpointHash == "" {
		return nil
	}
	runs, err := projectRuns(events)
	if err != nil {
		return nil
	}
	type candidate struct {
		id    string
		order int64
	}
	candidates := make([]candidate, 0)
	actorScope := ""
	if len(actorScopes) > 0 {
		actorScope = strings.TrimSpace(actorScopes[0])
	}
	for runID, run := range runs {
		if runID == currentRunID || !run.terminal || run.view.Status != RunFailed || run.view.CheckpointHash != checkpointHash {
			continue
		}
		if actorScope != "" && run.actor.ScopeKey() != actorScope {
			continue
		}
		candidates = append(candidates, candidate{id: runID, order: run.order})
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].order < candidates[right].order })
	ids := make([]string, 0, len(candidates))
	for _, item := range candidates {
		ids = append(ids, item.id)
	}
	return ids
}

func normalizedRetryRunIDs(primary string, candidates []string) []string {
	seen := make(map[string]struct{}, len(candidates)+1)
	ids := make([]string, 0, len(candidates)+1)
	appendID := func(raw string) {
		id := strings.TrimSpace(raw)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	appendID(primary)
	for _, candidate := range candidates {
		appendID(candidate)
	}
	return ids
}

func projectRun(events []Event, runID string) (runProjection, error) {
	runs, err := projectRuns(events)
	if err != nil {
		return runProjection{}, err
	}
	run, ok := runs[runID]
	if !ok {
		return runProjection{}, ErrRunNotFound
	}
	return run, nil
}

func projectRuns(events []Event) (map[string]runProjection, error) {
	runs := make(map[string]runProjection)
	for _, event := range events {
		switch event.Type {
		case continuationEventType:
			payload, err := decodeContinuationPayload(event.Payload)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid continuation payload at seq %d", ErrEventIntegrity, event.Seq)
			}
			// Pre-runner receipts remain immutable audit history but have no
			// schedulable run identity.
			if strings.TrimSpace(payload.RunID) == "" {
				continue
			}
			if _, exists := runs[payload.RunID]; exists {
				return nil, fmt.Errorf("%w: duplicate run id at seq %d", ErrEventIntegrity, event.Seq)
			}
			runs[payload.RunID] = runProjection{view: RunView{
				SessionID: event.SessionID, RunID: payload.RunID, RequestID: payload.RequestID,
				Status: RunQueued, CheckpointHash: payload.CheckpointHash,
			}, inputEvent: payload.InputEventID, surfaceHash: payload.SurfaceSHA256, targetSeq: payload.TargetSeq, actor: payload.Actor, order: event.Seq, createdOrder: event.Seq}
		case runLeasedEventType, runHeartbeatEventType:
			var payload runLeasePayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, fmt.Errorf("%w: invalid run lease at seq %d", ErrEventIntegrity, event.Seq)
			}
			run, exists := runs[payload.RunID]
			if !exists || run.terminal || payload.RequestID != run.view.RequestID || payload.Attempt < 1 || strings.TrimSpace(payload.LeaseID) == "" || strings.TrimSpace(payload.WorkerID) == "" || payload.LeaseUntil.IsZero() {
				return nil, fmt.Errorf("%w: invalid run lease transition at seq %d", ErrEventIntegrity, event.Seq)
			}
			if event.Type == runLeasedEventType {
				if payload.Attempt != run.view.Attempt+1 {
					return nil, fmt.Errorf("%w: invalid run attempt at seq %d", ErrEventIntegrity, event.Seq)
				}
				started := event.CreatedAt
				run.view.StartedAt = &started
			} else if payload.Attempt != run.view.Attempt || payload.LeaseID != run.leaseID {
				return nil, fmt.Errorf("%w: invalid run heartbeat at seq %d", ErrEventIntegrity, event.Seq)
			}
			leaseUntil := payload.LeaseUntil
			run.view.Status, run.view.Attempt, run.view.WorkerID, run.view.LeaseUntil = RunRunning, payload.Attempt, payload.WorkerID, &leaseUntil
			run.leaseID = payload.LeaseID
			run.order = event.Seq
			runs[payload.RunID] = run
		case runCompletedEventType, runFailedEventType:
			var payload runTerminalPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, fmt.Errorf("%w: invalid run terminal at seq %d", ErrEventIntegrity, event.Seq)
			}
			run, exists := runs[payload.RunID]
			if !exists || run.terminal || payload.RequestID != run.view.RequestID || payload.LeaseID != run.leaseID || payload.Attempt != run.view.Attempt {
				return nil, fmt.Errorf("%w: invalid run terminal transition at seq %d", ErrEventIntegrity, event.Seq)
			}
			completed := event.CreatedAt
			run.view.CompletedAt, run.view.LeaseUntil, run.terminal = &completed, nil, true
			if event.Type == runCompletedEventType {
				run.view.Status = RunCompleted
			} else {
				run.view.Status, run.view.Error = RunFailed, payload.Error
			}
			run.order = event.Seq
			runs[payload.RunID] = run
		}
	}
	// Derive retry lineage from the same immutable event stream. A retry only
	// points backwards to terminal failures for the same checkpoint and actor;
	// unrelated or future runs remain isolated.
	for runID, current := range runs {
		checkpointHash := strings.TrimSpace(current.view.CheckpointHash)
		if checkpointHash == "" {
			continue
		}
		candidates := make([]runProjection, 0)
		for candidateID, candidate := range runs {
			if candidateID == runID || !candidate.terminal || candidate.view.Status != RunFailed || candidate.view.CheckpointHash != checkpointHash || candidate.createdOrder >= current.createdOrder {
				continue
			}
			if current.actor.ScopeKey() != "" && candidate.actor.ScopeKey() != current.actor.ScopeKey() {
				continue
			}
			candidates = append(candidates, candidate)
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].createdOrder < candidates[j].createdOrder })
		if len(candidates) > 0 {
			ids := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				ids = append(ids, candidate.view.RunID)
			}
			current.view.RetryOfRunID = ids[0]
			current.view.RetryOfRunIDs = ids
			runs[runID] = current
		}
	}
	return runs, nil
}
