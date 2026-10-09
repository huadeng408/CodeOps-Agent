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

	codeagentpb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/orchestrator"
	"code-agent/internal/permission"
	"code-agent/internal/telemetry/genai"

	"go.opentelemetry.io/otel/trace"
)

const (
	runLeasedEventType     = "session/run-leased"
	runHeartbeatEventType  = "session/run-heartbeat"
	runCompletedEventType  = "session/run-completed"
	runFailedEventType     = "session/run-failed"
	runCanceledEventType   = "session/run-canceled"
	toolDispatchedType     = "tool/dispatched"
	toolUnknownEventType   = "tool/unknown"
	commitPendingEventType = "commit_pending"
	commitDoneEventType    = "commit_done"
	planTodoEventType      = "session/plan-todo"
	progressEventType      = "session/progress"
)

var (
	ErrRunNotFound         = errors.New("session run not found")
	ErrLeaseLost           = errors.New("session run lease lost")
	ErrSessionRunnerClosed = errors.New("session runner is closed")
)

// PartialRecoveryError means the runner recovered all valid sessions it could
// inspect, while one or more independent session histories failed integrity
// checks. Callers should keep the runner available and surface the error as a
// health warning instead of tearing down healthy continuation work.
type PartialRecoveryError struct{ Err error }

func (e *PartialRecoveryError) Error() string {
	if e == nil || e.Err == nil {
		return "partial session recovery failed"
	}
	details := partialRecoveryDetails(e.Err)
	if len(details) == 0 {
		return "partial session recovery failed"
	}
	const visibleDetails = 3
	visible := details
	if len(visible) > visibleDetails {
		visible = visible[:visibleDetails]
	}
	message := "partial session recovery failed: " + strings.Join(visible, "; ")
	if remaining := len(details) - len(visible); remaining > 0 {
		message += fmt.Sprintf("; and %d more", remaining)
	}
	return message
}

func (e *PartialRecoveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func partialRecoveryDetails(err error) []string {
	if err == nil {
		return nil
	}
	errorsToDescribe := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errorsToDescribe = joined.Unwrap()
	}
	details := make([]string, 0, len(errorsToDescribe))
	for _, item := range errorsToDescribe {
		if item == nil {
			continue
		}
		const maxRunes = 160
		runes := []rune(strings.TrimSpace(item.Error()))
		if len(runes) > maxRunes {
			runes = append(runes[:maxRunes-3], '.', '.', '.')
		}
		if len(runes) > 0 {
			details = append(details, string(runes))
		}
	}
	return details
}

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
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

// SubmitMessageCommand is one durable natural-language turn. RequestID binds
// the user message, its automatic recovery anchor, and the resulting run.
type SubmitMessageCommand struct {
	RequestID   string
	SessionID   string
	OwnerID     uint
	ExpectedSeq int64
	Content     string
	Actor       identity.Actor
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
	SubmitMessage(context.Context, SubmitMessageCommand) (RunView, error)
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

// WorkingDirToolExecutionAdapter is an optional seam for executors whose
// mutable cwd must be isolated per durable Session. Legacy adapters continue
// to use Execute; production continuation wiring implements this method.
type WorkingDirToolExecutionAdapter interface {
	ToolExecutionAdapter
	ExecuteInWorkingDir(context.Context, identity.Actor, string, string, orchestrator.ToolCall) orchestrator.ToolResult
}

// sessionResourceReleaser is optional. Agent executors implement it to drop
// per-session process state after a durable task reaches a terminal state;
// ReleaseSession must be idempotent because terminal replay is also idempotent.
type sessionResourceReleaser interface {
	ReleaseSession(string) error
}

// ToolExecutionFunc adapts the Harness-owned tool executor without exposing
// executor internals to the continuation module.
type ToolExecutionFunc func(context.Context, identity.Actor, string, orchestrator.ToolCall) orchestrator.ToolResult

func (f ToolExecutionFunc) Execute(ctx context.Context, actor identity.Actor, sessionID string, call orchestrator.ToolCall) orchestrator.ToolResult {
	return f(ctx, actor, sessionID, call)
}

func (r *SessionRunner) releaseSessionResources(sessionID string) {
	if releaser, ok := r.tools.(sessionResourceReleaser); ok {
		_ = releaser.ReleaseSession(sessionID)
	}
}

type SessionRunnerOptions struct {
	WorkerID          string
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	// ProgressInterval bounds visible narration for a slow model or tool. It
	// is separate from the lease heartbeat, which remains transport-only.
	ProgressInterval time.Duration
	QueueSize        int
	// WorkerCount controls bounded cross-session execution concurrency.
	// Runs from the same session remain serialized by the durable run CAS.
	WorkerCount    int
	Now            func() time.Time
	Permissions    *permission.Controller
	AgentSpawn     orchestrator.AgentSpawnHandler
	AgentLifecycle orchestrator.AgentLifecycleHandler
	Memory         SessionMemory
	AgentWorkspace func(context.Context, AgentWorkspaceRequest) (string, error)
	// AgentToolBinding resolves dynamic tools before delegation and again before
	// every child call. Static Agent Card tools remain local policy.
	AgentToolBinding func(string) (AgentToolBinding, bool)
	AgentApproval    func(context.Context, *codeagentpb.AgentToolApproval) (ApprovalDecision, error)
	AgentWorkerCount int
	Tracer           genai.Tracer
}

type SessionRunner struct {
	workbench       *Workbench
	conversation    ConversationAdapter
	tools           ToolExecutionAdapter
	options         SessionRunnerOptions
	ctx             context.Context
	cancel          context.CancelFunc
	queue           chan runKey
	agentQueue      chan runKey
	memoryQueue     chan runKey
	agentMu         sync.Mutex
	activeAgentRuns map[runKey]context.CancelFunc
	// agentLifecycleMu serializes callback-plus-acknowledgement so concurrent
	// terminal observers cannot clean the same managed workspace twice.
	agentLifecycleMu sync.Mutex
	queuedMu         sync.Mutex
	queued           map[runKey]struct{}
	queuedContexts   map[runKey]context.Context
	lifecycleMu      sync.RWMutex
	closed           bool
	// admissionClosed closes the WaitGroup admission gate before Close waits.
	// It is atomic because enqueue can be called from a worker while a public
	// request still holds lifecycleMu.RLock.
	admissionClosed atomic.Bool
	enqueueMu       sync.Mutex
	wg              sync.WaitGroup
}

type runKey struct {
	sessionID      string
	runID          string
	memoryRecovery bool
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

// commitPayload binds one side-effect attempt to the append-only Ledger.
// A pending record is written before dispatch; the matching done record is
// written only while closing the run. If a process dies in between, recovery
// sees the pending record and must not execute the mutating tool again.
type commitPayload struct {
	CommitID        string `json:"commit_id"`
	RunID           string `json:"run_id"`
	ToolCallID      string `json:"tool_call_id"`
	ToolName        string `json:"tool_name"`
	ArgumentsJSON   string `json:"arguments_json,omitempty"`
	PendingSeq      int64  `json:"pending_seq,omitempty"`
	PendingChecksum string `json:"pending_checksum,omitempty"`
}

// planTodoPayload is the canonical, whole-value Plan/Todo projection emitted
// by the orchestrator. It is a ledger fact (not a mutable side store), so a
// reopened Session can reconstruct exactly the state sent to the next turn.
type planTodoPayload struct {
	RunID    string        `json:"run_id"`
	Revision uint64        `json:"revision"`
	Plan     planPayload   `json:"plan"`
	Todos    []todoPayload `json:"todos"`
}

type planPayload struct {
	Steps        []string `json:"steps,omitempty"`
	CurrentIndex int      `json:"current_index"`
	Mode         string   `json:"mode,omitempty"`
}

type todoPayload struct {
	Content    string `json:"content"`
	ActiveForm string `json:"active_form,omitempty"`
	Status     string `json:"status"`
}

// progressPayload is a short, browser-safe description of a durable run
// stage. It deliberately has no tool arguments, output, patches, or provider
// error text: the related ledger facts retain their audit detail separately.
type progressPayload struct {
	RunID          string `json:"run_id"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	Summary        string `json:"summary"`
	PlanRevision   uint64 `json:"plan_revision,omitempty"`
	TodoRevision   uint64 `json:"todo_revision,omitempty"`
	SourceEventSeq int64  `json:"source_event_seq"`
}

const (
	progressPhase     = "phase"
	progressMilestone = "milestone"
	progressNarration = "narration"
)

func validProgressKind(kind string) bool {
	switch kind {
	case progressPhase, progressMilestone, progressNarration:
		return true
	default:
		return false
	}
}

func boundedProgressText(value string, maximum int) (string, bool) {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" || len([]rune(value)) > maximum {
		return "", false
	}
	return value, true
}

func validateProgressPayload(payload progressPayload) (progressPayload, error) {
	payload.RunID = strings.TrimSpace(payload.RunID)
	if payload.RunID == "" || !validProgressKind(payload.Kind) || payload.SourceEventSeq < 0 {
		return progressPayload{}, errors.New("invalid progress payload")
	}
	var ok bool
	if payload.Title, ok = boundedProgressText(payload.Title, 80); !ok {
		return progressPayload{}, errors.New("invalid progress title")
	}
	if payload.Summary, ok = boundedProgressText(payload.Summary, 240); !ok {
		return progressPayload{}, errors.New("invalid progress summary")
	}
	return payload, nil
}

func progressToolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 48 {
		return "工具"
	}
	for _, runeValue := range name {
		if !((runeValue >= 'a' && runeValue <= 'z') || (runeValue >= 'A' && runeValue <= 'Z') || (runeValue >= '0' && runeValue <= '9') || runeValue == '_' || runeValue == '-') {
			return "工具"
		}
	}
	return name
}

func todoProgressSummary(payload planTodoPayload) string {
	pending, active, completed := 0, 0, 0
	for _, item := range payload.Todos {
		switch item.Status {
		case "completed":
			completed++
		case "in_progress":
			active++
		default:
			pending++
		}
	}
	return fmt.Sprintf("任务进度已更新：%d 待处理，%d 进行中，%d 已完成。", pending, active, completed)
}

func planTodoFromPayload(payload planTodoPayload) *codeagentpb.PlanTodoSnapshot {
	plan := &codeagentpb.PlanUpdate{
		Steps:        append([]string(nil), payload.Plan.Steps...),
		CurrentIndex: int32(payload.Plan.CurrentIndex),
		Mode:         payload.Plan.Mode,
		Revision:     payload.Revision,
	}
	todos := make([]*codeagentpb.TodoItem, 0, len(payload.Todos))
	for _, item := range payload.Todos {
		todos = append(todos, &codeagentpb.TodoItem{Content: item.Content, ActiveForm: item.ActiveForm, Status: item.Status})
	}
	return &codeagentpb.PlanTodoSnapshot{SchemaVersion: 1, Revision: payload.Revision, Plan: plan, Todos: todos}
}

func latestPlanTodo(events []Event) (*codeagentpb.PlanTodoSnapshot, error) {
	var latest planTodoPayload
	for _, event := range events {
		if event.Type != "session/plan-todo" {
			continue
		}
		var payload planTodoPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, fmt.Errorf("%w: invalid plan/todo payload at seq %d", ErrEventIntegrity, event.Seq)
		}
		if payload.Revision == 0 || payload.Revision <= latest.Revision {
			return nil, fmt.Errorf("%w: non-monotonic plan/todo revision at seq %d", ErrEventIntegrity, event.Seq)
		}
		latest = payload
	}
	if latest.Revision == 0 {
		return nil, nil
	}
	return planTodoFromPayload(latest), nil
}

func planTodoPayloadFromUpdates(events []Event, runID string, plan *codeagentpb.PlanUpdate, todo *codeagentpb.TodoUpdate) (planTodoPayload, error) {
	latest, err := latestPlanTodo(events)
	if err != nil {
		return planTodoPayload{}, err
	}
	payload := planTodoPayload{RunID: strings.TrimSpace(runID), Revision: 0, Plan: planPayload{Mode: "chat"}}
	if latest != nil {
		payload.Revision = latest.GetRevision()
		if latest.GetPlan() != nil {
			payload.Plan = planPayload{Steps: append([]string(nil), latest.GetPlan().GetSteps()...), CurrentIndex: int(latest.GetPlan().GetCurrentIndex()), Mode: latest.GetPlan().GetMode()}
		}
		for _, item := range latest.GetTodos() {
			payload.Todos = append(payload.Todos, todoPayload{Content: item.GetContent(), ActiveForm: item.GetActiveForm(), Status: item.GetStatus()})
		}
	}
	if plan != nil {
		payload.Plan = planPayload{Steps: append([]string(nil), plan.GetSteps()...), CurrentIndex: int(plan.GetCurrentIndex()), Mode: plan.GetMode()}
	}
	if todo != nil {
		payload.Todos = payload.Todos[:0]
		for _, item := range todo.GetTodos() {
			payload.Todos = append(payload.Todos, todoPayload{Content: item.GetContent(), ActiveForm: item.GetActiveForm(), Status: item.GetStatus()})
		}
	}
	revision := uint64(0)
	if plan != nil {
		revision = plan.GetRevision()
	}
	if todo != nil {
		revision = todo.GetRevision()
	}
	if revision == 0 {
		revision = payload.Revision + 1
	}
	if revision != payload.Revision+1 {
		return planTodoPayload{}, fmt.Errorf("%w: plan/todo revision %d follows %d", ErrEventIntegrity, revision, payload.Revision)
	}
	payload.Revision = revision
	if payload.Plan.Mode == "" {
		payload.Plan.Mode = "chat"
	}
	return payload, nil
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
	if options.ProgressInterval <= 0 {
		options.ProgressInterval = 10 * time.Second
	}
	if options.QueueSize <= 0 {
		options.QueueSize = 256
	}
	if options.WorkerCount <= 0 {
		options.WorkerCount = 4
	}
	if options.WorkerCount > 6 {
		options.WorkerCount = 6
	}
	if options.AgentWorkerCount <= 0 || options.AgentWorkerCount > 4 {
		options.AgentWorkerCount = 4
	}
	// Reserve one of the ten execution slots for terminal memory recovery.
	if options.WorkerCount+options.AgentWorkerCount > 9 {
		options.WorkerCount = 9 - options.AgentWorkerCount
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := &SessionRunner{
		workbench: workbench, conversation: conversation, tools: tools, options: options,
		ctx: ctx, cancel: cancel, queue: make(chan runKey, options.QueueSize), queued: make(map[runKey]struct{}), queuedContexts: make(map[runKey]context.Context),
		agentQueue: make(chan runKey, options.QueueSize), activeAgentRuns: make(map[runKey]context.CancelFunc),
		memoryQueue: make(chan runKey, options.QueueSize),
	}
	runner.wg.Add(options.WorkerCount + options.AgentWorkerCount + 1)
	for i := 0; i < options.WorkerCount; i++ {
		go runner.workerLoop(runner.queue)
	}
	for i := 0; i < options.AgentWorkerCount; i++ {
		go runner.workerLoop(runner.agentQueue)
	}
	go runner.workerLoop(runner.memoryQueue)
	return runner
}

func (r *SessionRunner) RequestContinuation(ctx context.Context, command ContinueCommand) (RunView, error) {
	if r == nil {
		return RunView{}, errors.New("session runner requires a workbench")
	}
	r.lifecycleMu.RLock()
	defer r.lifecycleMu.RUnlock()
	return r.requestContinuation(ctx, command, true)
}

func (r *SessionRunner) requestContinuation(ctx context.Context, command ContinueCommand, requirePaused bool) (RunView, error) {
	if r == nil || r.workbench == nil || r.workbench.ledger == nil {
		return RunView{}, errors.New("session runner requires a workbench")
	}
	if r.closed {
		return RunView{}, ErrSessionRunnerClosed
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
		r.enqueue(ctx, runKey{sessionID: command.SessionID, runID: existing.view.RunID}, time.Time{})
		return existing.view, nil
	}
	current, err := reduceSessionView(events)
	if err != nil {
		return RunView{}, err
	}
	if requirePaused && current.Status != "paused" {
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
					r.enqueue(ctx, runKey{sessionID: command.SessionID, runID: existing.view.RunID}, time.Time{})
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
	r.enqueue(ctx, runKey{sessionID: command.SessionID, runID: runID}, time.Time{})
	return view, nil
}

// SubmitMessage persists and starts one ordinary user turn without exposing
// checkpoint choreography to the caller. Every durable stage is keyed by the
// same request ID, so a retry after interruption resumes instead of duplicating
// the message or creating an unrelated run.
func (r *SessionRunner) SubmitMessage(ctx context.Context, command SubmitMessageCommand) (RunView, error) {
	if r == nil || r.workbench == nil || r.workbench.ledger == nil {
		return RunView{}, errors.New("session runner requires a workbench")
	}
	r.lifecycleMu.RLock()
	defer r.lifecycleMu.RUnlock()
	if r.closed {
		return RunView{}, ErrSessionRunnerClosed
	}
	if r.conversation == nil {
		return RunView{}, errors.New("session runner requires an orchestrator")
	}
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.Content = strings.TrimSpace(command.Content)
	if command.RequestID == "" || command.SessionID == "" || command.Content == "" || command.ExpectedSeq < 0 {
		return RunView{}, fmt.Errorf("%w: requestId, sessionId, content, and expectedSeq are required", ErrInvalidSessionInput)
	}
	if _, err := r.workbench.Get(ctx, command.OwnerID, command.SessionID); err != nil {
		return RunView{}, err
	}
	actor, err := command.Actor.BindSession(command.SessionID)
	if err != nil {
		return RunView{}, fmt.Errorf("%w: invalid message actor", ErrInvalidSessionInput)
	}

	for attempt := 0; attempt < 32; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, command.SessionID)
		if err != nil {
			return RunView{}, err
		}
		message, boundMessage, messageExists, err := userMessageForRequest(events, command.RequestID)
		if err != nil {
			return RunView{}, err
		}
		if messageExists && boundMessage.Content != command.Content {
			return RunView{}, fmt.Errorf("%w: requestId is already bound to different message content", ErrSessionStateConflict)
		}
		if existing, ok, projectErr := runForRequest(events, command.RequestID); projectErr != nil {
			return RunView{}, projectErr
		} else if ok {
			if !messageExists || existing.actor.ScopeKey() != actor.ScopeKey() {
				return RunView{}, fmt.Errorf("%w: requestId is already bound to another turn", ErrSessionStateConflict)
			}
			r.enqueue(ctx, runKey{sessionID: command.SessionID, runID: existing.view.RunID}, time.Time{})
			return existing.view, nil
		}
		if active, projectErr := hasActiveRun(events); projectErr != nil {
			return RunView{}, projectErr
		} else if active {
			return RunView{}, fmt.Errorf("%w: another session run is active", ErrSessionStateConflict)
		}
		if !messageExists {
			if int64(len(events)) != command.ExpectedSeq {
				return RunView{}, fmt.Errorf("%w: session=%s expected=%d actual=%d", ErrSequenceConflict, command.SessionID, command.ExpectedSeq, len(events))
			}
			_, err = r.workbench.ledger.AppendSurface(ctx, command.SessionID, command.ExpectedSeq, userMessageEventType, messagePayload{
				Author: "user", Content: command.Content, RequestID: command.RequestID,
			}, SurfaceOperation{Op: "append"})
			if errors.Is(err, ErrSequenceConflict) {
				continue
			}
			if err != nil {
				return RunView{}, err
			}
			r.workbench.signal(command.SessionID)
			continue
		}

		checkpoint, boundCheckpoint, checkpointExists, err := automaticCheckpointForRequest(events, command.RequestID)
		if err != nil {
			return RunView{}, err
		}
		if checkpointExists {
			if boundCheckpoint.TargetEventID != message.EventID || boundCheckpoint.TargetChecksum != message.Checksum || boundCheckpoint.TargetSeq != message.Seq {
				return RunView{}, fmt.Errorf("%w: automatic checkpoint changed message identity", ErrEventIntegrity)
			}
		} else {
			surface, surfaceErr := r.workbench.ledger.Surface(ctx, command.SessionID)
			if surfaceErr != nil {
				return RunView{}, surfaceErr
			}
			if len(surface) == 0 || surface[len(surface)-1].EventID != message.EventID {
				return RunView{}, fmt.Errorf("%w: another user turn changed the active surface", ErrSessionStateConflict)
			}
			payload := checkpointPayload{
				TargetEventID: message.EventID, TargetSeq: message.Seq, TargetChecksum: message.Checksum,
				Label: "automatic user turn", RequestID: command.RequestID, Automatic: true,
			}
			checkpoint, err = r.workbench.ledger.Append(ctx, command.SessionID, int64(len(events)), checkpointEventType, payload)
			if errors.Is(err, ErrSequenceConflict) {
				continue
			}
			if err != nil {
				return RunView{}, err
			}
			r.workbench.signal(command.SessionID)
			continue
		}

		return r.requestContinuation(ctx, ContinueCommand{
			RequestID: command.RequestID, SessionID: command.SessionID, CheckpointHash: checkpoint.Checksum,
			OwnerID: command.OwnerID, ExpectedSeq: int64(len(events)), Actor: actor,
		}, false)
	}
	return RunView{}, ErrSequenceConflict
}

func userMessageForRequest(events []Event, requestID string) (Event, messagePayload, bool, error) {
	var found Event
	var foundPayload messagePayload
	foundOne := false
	for _, event := range events {
		if event.Type != userMessageEventType {
			continue
		}
		var payload messagePayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return Event{}, messagePayload{}, false, fmt.Errorf("%w: invalid user message at seq %d", ErrEventIntegrity, event.Seq)
		}
		if payload.RequestID != requestID {
			continue
		}
		if foundOne {
			return Event{}, messagePayload{}, false, fmt.Errorf("%w: duplicate message requestId", ErrEventIntegrity)
		}
		found, foundPayload, foundOne = event, payload, true
	}
	return found, foundPayload, foundOne, nil
}

func automaticCheckpointForRequest(events []Event, requestID string) (Event, checkpointPayload, bool, error) {
	var found Event
	var foundPayload checkpointPayload
	foundOne := false
	for _, event := range events {
		if event.Type != checkpointEventType {
			continue
		}
		var payload checkpointPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return Event{}, checkpointPayload{}, false, fmt.Errorf("%w: invalid checkpoint at seq %d", ErrEventIntegrity, event.Seq)
		}
		if !payload.Automatic || payload.RequestID != requestID {
			continue
		}
		if foundOne {
			return Event{}, checkpointPayload{}, false, fmt.Errorf("%w: duplicate automatic checkpoint requestId", ErrEventIntegrity)
		}
		found, foundPayload, foundOne = event, payload, true
	}
	return found, foundPayload, foundOne, nil
}

func hasActiveRun(events []Event) (bool, error) {
	runs, err := projectRuns(events)
	if err != nil {
		return false, err
	}
	for _, run := range runs {
		if !run.terminal {
			return true, nil
		}
	}
	return false, nil
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
	r.lifecycleMu.RLock()
	if r.closed {
		r.lifecycleMu.RUnlock()
		return ErrSessionRunnerClosed
	}
	r.lifecycleMu.RUnlock()
	ids, err := r.workbench.ledger.SessionIDs(ctx)
	if err != nil {
		return err
	}
	var recoverErrs []error
	for _, sessionID := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		events, readErr := r.workbench.ledger.Events(ctx, sessionID)
		if readErr != nil {
			recoverErrs = append(recoverErrs, fmt.Errorf("recover session %s events: %w", sessionID, readErr))
			continue
		}
		// SessionIDs is a read-side union of canonical ledger IDs and legacy
		// snapshot IDs. A legacy-only Session has no canonical facts until an
		// explicit import, so it has no run for this module to recover.
		if len(events) == 0 {
			continue
		}
		if isVerificationAdmissionStream(events) {
			continue
		}
		// The older event-store module shares the physical ledger but owns a
		// different projection and lifecycle. Its streams are not runner work;
		// unknown non-Workbench streams still fail below instead of being hidden.
		switch events[0].Type {
		case sessionStateEventType, "legacy/import", "session/fork/import", "memory/catalog-created":
			continue
		}
		// A deleted session remains in the append-only ledger for auditability,
		// but its unfinished runs must never be resurrected after a process
		// restart. Re-project the lifecycle state before scheduling any work so
		// deletion is a durable execution fence rather than only a UI tombstone.
		view, viewErr := reduceSessionView(events)
		if viewErr != nil {
			recoverErrs = append(recoverErrs, fmt.Errorf("recover session %s projection: %w", sessionID, viewErr))
			continue
		}
		if view.Status == "deleted" {
			continue
		}
		if err := r.recoverAgentTask(ctx, view.UserID, events); err != nil {
			recoverErrs = append(recoverErrs, fmt.Errorf("recover session %s agent task: %w", sessionID, err))
			continue
		}
		events, readErr = r.workbench.ledger.Events(ctx, sessionID)
		if readErr != nil {
			recoverErrs = append(recoverErrs, fmt.Errorf("recover session %s updated events: %w", sessionID, readErr))
			continue
		}
		runs, projectErr := projectRuns(events)
		if projectErr != nil {
			recoverErrs = append(recoverErrs, fmt.Errorf("recover session %s runs: %w", sessionID, projectErr))
			continue
		}
		for runID, run := range runs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if run.terminal {
				// Repair only the latest terminal run through the existing workers;
				// provider reflection must not consume the startup recovery window.
				if view.Run == nil || view.Run.RunID != runID {
					continue
				}
			}
			wakeAt := time.Time{}
			memoryRecovery := run.terminal
			if memoryRecovery {
				state, retryAt, found := latestMemoryCommitState(events, runID)
				if found && state == "permanently_blocked" {
					continue
				}
				if found && retryAt.After(r.now()) {
					wakeAt = retryAt
				}
			}
			if !run.terminal && run.view.LeaseUntil != nil && run.view.LeaseUntil.After(r.now()) {
				wakeAt = *run.view.LeaseUntil
			}
			r.lifecycleMu.RLock()
			if r.closed {
				r.lifecycleMu.RUnlock()
				return ErrSessionRunnerClosed
			}
			r.enqueue(ctx, runKey{sessionID: sessionID, runID: runID, memoryRecovery: memoryRecovery}, wakeAt)
			r.lifecycleMu.RUnlock()
		}
	}
	if err := errors.Join(recoverErrs...); err != nil {
		return &PartialRecoveryError{Err: err}
	}
	return nil
}

func (r *SessionRunner) Close() error {
	if r == nil {
		return nil
	}
	r.lifecycleMu.Lock()
	if r.closed {
		r.lifecycleMu.Unlock()
		return nil
	}
	r.closed = true
	r.admissionClosed.Store(true)
	cancel := r.cancel
	r.lifecycleMu.Unlock()
	// Drain any enqueue already inside the admission section before waiting on
	// the group. This prevents a worker from calling Add after Wait begins.
	r.enqueueMu.Lock()
	r.enqueueMu.Unlock()
	if cancel != nil {
		cancel()
		r.wg.Wait()
	}
	r.queuedMu.Lock()
	defer r.queuedMu.Unlock()
	clear(r.queued)
	clear(r.queuedContexts)
	for _, queue := range []chan runKey{r.queue, r.agentQueue, r.memoryQueue} {
		for len(queue) > 0 {
			<-queue
		}
	}
	return nil
}

func (r *SessionRunner) now() time.Time { return r.options.Now().UTC() }

func (r *SessionRunner) enqueue(ctx context.Context, key runKey, wakeAt time.Time) {
	if r.admissionClosed.Load() {
		return
	}
	r.enqueueMu.Lock()
	defer r.enqueueMu.Unlock()
	if r.admissionClosed.Load() {
		return
	}
	traceCtx := genai.DetachedTraceContext(ctx, key.sessionID)
	r.queuedMu.Lock()
	if r.queued == nil {
		r.queued = make(map[runKey]struct{})
	}
	if r.queuedContexts == nil {
		r.queuedContexts = make(map[runKey]context.Context)
	}
	if _, exists := r.queued[key]; exists {
		queuedCtx := r.queuedContexts[key]
		if (queuedCtx == nil || !trace.SpanContextFromContext(queuedCtx).IsValid()) && trace.SpanContextFromContext(traceCtx).IsValid() {
			r.queuedContexts[key] = traceCtx
		}
		r.queuedMu.Unlock()
		return
	}
	r.queued[key] = struct{}{}
	r.queuedContexts[key] = traceCtx
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
				_ = r.releaseQueued(key)
				return
			}
		}
		select {
		case r.targetQueue(key) <- key:
		case <-r.ctx.Done():
			_ = r.releaseQueued(key)
		}
	}()
}

func (r *SessionRunner) releaseQueued(key runKey) context.Context {
	r.queuedMu.Lock()
	ctx := r.queuedContexts[key]
	delete(r.queued, key)
	delete(r.queuedContexts, key)
	r.queuedMu.Unlock()
	if ctx == nil {
		return genai.DetachedTraceContext(nil, key.sessionID)
	}
	return ctx
}

func (r *SessionRunner) targetQueue(key runKey) chan runKey {
	if key.memoryRecovery {
		return r.memoryQueue
	}
	if strings.HasPrefix(key.sessionID, "agent-") {
		return r.agentQueue
	}
	return r.queue
}

func (r *SessionRunner) workerLoop(queue chan runKey) {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case key := <-queue:
			traceCtx := r.releaseQueued(key)
			r.execute(traceCtx, key)
		}
	}
}

func (r *SessionRunner) execute(traceCtx context.Context, key runKey) {
	executionCtx, cancelExecution := context.WithCancel(traceCtx)
	stopRunnerCancel := context.AfterFunc(r.ctx, cancelExecution)
	defer stopRunnerCancel()
	defer cancelExecution()
	traceCtx = executionCtx
	events, err := r.workbench.ledger.Events(traceCtx, key.sessionID)
	if err != nil {
		return
	}
	run, err := projectRun(events, key.runID)
	if err != nil {
		return
	}
	if key.memoryRecovery && !run.terminal {
		return
	}
	if run.terminal {
		view, err := reduceSessionView(events)
		if err == nil && view.Status != "deleted" && view.Run != nil && view.Run.RunID == key.runID {
			r.completeAgentTurn(traceCtx, key)
			r.commitSessionMemory(traceCtx, key)
		}
		return
	}
	completionCtx := traceCtx
	defer func() { r.completeAgentTurn(completionCtx, key) }()
	deletions, stopWatching := r.workbench.watchDeletion(key.sessionID)
	defer stopWatching()
	lease, waitUntil, err := r.claim(traceCtx, key)
	if err != nil {
		return
	}
	if !waitUntil.IsZero() {
		r.enqueue(traceCtx, key, waitUntil)
		return
	}
	spanName := "agent.main"
	agentName := "main"
	if strings.HasPrefix(key.sessionID, "agent-") {
		spanName, agentName = "agent.subagent", "subagent"
	}
	var agentSpan genai.Span
	if r.options.Tracer != nil {
		traceCtx, agentSpan = r.options.Tracer.StartSpan(traceCtx, spanName, genai.OperationInvokeAgent, genai.SystemGenAI)
		completionCtx = traceCtx
		agentSpan.SetAttributes(genai.AgentNameKV(agentName))
		defer agentSpan.End()
	}
	r.recordProgress(traceCtx, key, lease, progressPhase, "开始执行", "已开始分析本次请求并准备执行。", 0, 0)
	if committed, committedErr := r.hasCommittedAssistant(traceCtx, key); committedErr != nil {
		genai.MarkSpanError(agentSpan)
		r.fail(key, lease, committedErr)
		return
	} else if committed {
		r.recordProgress(traceCtx, key, lease, progressMilestone, "恢复完成", "已恢复已提交的回复，正在完成本次运行。", 0, 0)
		if r.appendTerminal(traceCtx, key, lease, runCompletedEventType, "") == nil {
			r.commitSessionMemory(traceCtx, key)
		}
		return
	}
	runCtx, cancel := context.WithCancel(traceCtx)
	defer cancel()
	if strings.HasPrefix(key.sessionID, "agent-") {
		r.agentMu.Lock()
		r.activeAgentRuns[key] = cancel
		r.agentMu.Unlock()
		defer func() { r.agentMu.Lock(); delete(r.activeAgentRuns, key); r.agentMu.Unlock() }()
	}
	heartbeatDone := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go r.heartbeat(runCtx, key, lease, deletions, heartbeatDone, cancel, heartbeatErr)

	request, err := r.conversationRequest(runCtx, key, lease)
	if err != nil {
		cancel()
		<-heartbeatDone
		genai.MarkSpanError(agentSpan)
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
		genai.MarkSpanError(agentSpan)
		r.fail(key, lease, errors.New("continuation has no input or history"))
		return
	}
	if request.AgentTask != nil {
		parentDeleted, stopWatchingParent := r.workbench.watchDeletion(request.AgentTask.ParentSessionId)
		parentWatcherDone := make(chan struct{})
		defer func() { cancel(); <-parentWatcherDone; stopWatchingParent() }()
		go func() {
			defer close(parentWatcherDone)
			select {
			case <-runCtx.Done():
			case <-parentDeleted:
				fenceCtx, fenceCancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = r.cancelAgentTask(fenceCtx, request.AgentTask, "parent-deleted")
				fenceCancel()
				cancel()
			}
		}()
	}
	var blocked atomic.Bool
	retryRunIDs := normalizedRetryRunIDs(request.RetryOfRunID, request.RetryOfRunIDs)
	handlers := orchestrator.ConversationHandlers{
		AgentSpawn:     r.options.AgentSpawn,
		AgentLifecycle: r.options.AgentLifecycle,
		Compaction: func(update *codeagentpb.CompactionUpdate) error {
			if err := r.persistCompaction(runCtx, key, lease, update); err != nil {
				return err
			}
			r.recordProgress(runCtx, key, lease, progressMilestone, "整理上下文", "已整理较早对话，继续处理当前任务。", 0, 0)
			return nil
		},
		PlanTodo: func(plan *codeagentpb.PlanUpdate, todo *codeagentpb.TodoUpdate) error {
			events, err := r.workbench.ledger.Events(runCtx, key.sessionID)
			if err != nil {
				return err
			}
			payload, err := planTodoPayloadFromUpdates(events, key.runID, plan, todo)
			if err != nil {
				return err
			}
			if err := r.appendLeasedFact(runCtx, key, lease.LeaseID, planTodoEventType, payload); err != nil {
				return err
			}
			r.recordProgress(runCtx, key, lease, progressMilestone, "更新任务进度", todoProgressSummary(payload), payload.Revision, payload.Revision)
			return nil
		},
		Tool: func(ctx context.Context, call orchestrator.ToolCall) orchestrator.ToolResult {
			result, unsafe := r.executeTool(ctx, key, lease, request.Actor, request.WorkingDir, call, retryRunIDs...)
			if unsafe {
				blocked.Store(true)
			}
			return result
		},
	}
	result, runErr := r.conversation.RunConversation(runCtx, request, handlers)
	postRunCtx := context.WithoutCancel(runCtx)
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
	if r.ctx.Err() != nil && (runErr == nil || orchestrator.IsCanceledError(runErr)) {
		return
	}
	// A broken gRPC transport is not a terminal Agent outcome. Keep the
	// current lease as the fencing record and retry the same durable run only
	// after it expires; Python can then resume its run-bound checkpoint.
	if runErr != nil && orchestrator.IsConnectionError(runErr) {
		if retryErr := r.requeueAfterLease(postRunCtx, key, lease); retryErr == nil {
			return
		}
	}
	if runErr != nil || !result.Success || blocked.Load() {
		if runErr == nil {
			runErr = errors.New("orchestrator returned an unsuccessful terminal state")
		}
		genai.MarkSpanError(agentSpan)
		r.fail(key, lease, runErr)
		return
	}
	message := strings.TrimSpace(result.Message)
	if message == "" {
		genai.MarkSpanError(agentSpan)
		r.fail(key, lease, errors.New("orchestrator returned an empty response"))
		return
	}
	if err := r.appendLeasedSurface(postRunCtx, key, lease, "assistant/message", messagePayload{
		Author: "assistant", Content: message, RunID: key.runID,
	}); err != nil {
		genai.MarkSpanError(agentSpan)
		r.fail(key, lease, err)
		return
	}
	if err := r.appendTerminal(postRunCtx, key, lease, runCompletedEventType, ""); err != nil {
		genai.MarkSpanError(agentSpan)
		r.fail(key, lease, err)
		return
	}
	r.recordCompletedProgress(postRunCtx, key)
	r.completeAgentTurn(postRunCtx, key)
	r.commitSessionMemory(postRunCtx, key)
}

func (r *SessionRunner) requeueAfterLease(parent context.Context, key runKey, lease runLeasePayload) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
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
	r.enqueue(ctx, key, wakeAt)
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
		view, viewErr := reduceSessionView(events)
		if viewErr != nil {
			return runLeasePayload{}, time.Time{}, viewErr
		}
		if view.Status == "deleted" {
			return runLeasePayload{}, time.Time{}, ErrSessionNotFound
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

func (r *SessionRunner) heartbeat(ctx context.Context, key runKey, lease runLeasePayload, deletions <-chan struct{}, done chan<- struct{}, cancel context.CancelFunc, failure chan<- error) {
	defer close(done)
	ticker := time.NewTicker(r.options.HeartbeatInterval)
	defer ticker.Stop()
	lastNarration := r.now()
	narrated := false
	fail := func(err error) {
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return
		}
		select {
		case failure <- err:
		default:
		}
		cancel()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-deletions:
			events, err := r.workbench.ledger.Events(ctx, key.sessionID)
			if err != nil {
				fail(err)
				return
			}
			view, err := reduceSessionView(events)
			if err != nil {
				fail(err)
				return
			}
			if view.Status == "deleted" {
				fail(ErrSessionNotFound)
				return
			}
		case <-ticker.C:
			lease.LeaseUntil = r.now().Add(r.options.LeaseDuration)
			if err := r.appendLeasedFact(ctx, key, lease.LeaseID, runHeartbeatEventType, lease); err != nil {
				fail(err)
				return
			}
			if !narrated && r.now().Sub(lastNarration) >= r.options.ProgressInterval {
				// This is deliberately not a heartbeat event: it is a bounded,
				// user-visible narration for a genuinely slow operation.
				r.recordProgress(ctx, key, lease, progressNarration, "仍在执行", "任务仍在执行，正在等待模型或工具结果。", 0, 0)
				narrated = true
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
	workingDir := ""
	if len(events) > 0 && events[0].Type == sessionCreatedEventType {
		var created sessionCreatedPayload
		if err := json.Unmarshal(events[0].Payload, &created); err != nil {
			return orchestrator.ConversationRequest{}, fmt.Errorf("%w: invalid session working directory", ErrEventIntegrity)
		}
		workingDir = strings.TrimSpace(created.WorkingDir)
	}
	requestRetryOfRunID := ""
	retryOfRunIDs := []string(nil)
	if projection.surfaceHash != "" {
		checkpointSurface, surfaceErr := surfaceAt(events, projection.targetSeq)
		if surfaceErr != nil || projection.surfaceHash != checksumSurface(checkpointSurface) {
			return orchestrator.ConversationRequest{}, fmt.Errorf("%w: continuation surface has changed", ErrEventIntegrity)
		}
		requestRetryOfRunID = projection.view.RetryOfRunID
		retryOfRunIDs = append([]string(nil), projection.view.RetryOfRunIDs...)
		expanded, expandErr := expandContinuationCompactions(checkpointSurface, surface, events, append([]string{key.runID}, retryOfRunIDs...)...)
		if expandErr != nil {
			return orchestrator.ConversationRequest{}, expandErr
		}
		if err := validateContinuationSurface(checkpointSurface, expanded, key.runID, retryOfRunIDs...); err != nil {
			return orchestrator.ConversationRequest{}, err
		}
	}
	inputEventID := projection.inputEvent
	if strings.TrimSpace(inputEventID) == "" {
		// Legacy continuation receipts may omit input_event_id. Recover only an
		// explicit managed-worktree/SpawnAgent intent; ordinary history-only
		// checkpoints must keep Input empty so their user message remains in the
		// canonical history transcript.
		for index := len(surface) - 1; index >= 0; index-- {
			if surface[index].Type != userMessageEventType {
				continue
			}
			var payload messagePayload
			if json.Unmarshal(surface[index].Payload, &payload) == nil && explicitSpawnAgentIntent(payload.Content) {
				inputEventID = surface[index].EventID
			}
			break
		}
	}
	history, input, err := conversationHistory(surface, events, key.runID, inputEventID)
	if err != nil {
		return orchestrator.ConversationRequest{}, err
	}
	newTurn := false
	if len(retryOfRunIDs) == 0 {
		for _, event := range events {
			if event.EventID == projection.inputEvent && event.Type == userMessageEventType {
				var payload messagePayload
				if json.Unmarshal(event.Payload, &payload) != nil {
					return orchestrator.ConversationRequest{}, ErrEventIntegrity
				}
				newTurn = payload.RequestID != "" && payload.RequestID == projection.view.RequestID
			}
		}
	}
	planTodo, err := latestPlanTodo(events)
	if err != nil {
		return orchestrator.ConversationRequest{}, err
	}
	request := orchestrator.ConversationRequest{
		Input: input, SessionID: key.sessionID, WorkingDir: workingDir, RunID: key.runID, Resume: true, NewTurn: newTurn, SurfaceSHA256: projection.surfaceHash,
		RetryOfRunID: requestRetryOfRunID, RetryOfRunIDs: retryOfRunIDs,
		Actor: projection.actor, History: history, State: planTodo,
		HarnessManaged: true,
	}
	if workingDir != "" {
		request.ContextEnvelope, err = buildContextEnvelope(workingDir, events)
		if err != nil {
			return orchestrator.ConversationRequest{}, err
		}
	}
	if err := r.agentRequest(events, &request); err != nil {
		return orchestrator.ConversationRequest{}, err
	}
	if request.AgentTask == nil && r.options.Memory != nil && strings.TrimSpace(input) != "" && r.options.Permissions != nil && r.options.Permissions.CheckFor(request.Actor, "RecallMemory", map[string]any{"query": input}) == permission.Approve {
		request.MemoryContextJSON, _ = r.options.Memory.Recall(ctx, projectionOwner(events), input, 1200)
	}
	return request, nil
}

func explicitSpawnAgentIntent(content string) bool {
	value := strings.ToLower(strings.TrimSpace(content))
	return strings.Contains(value, "spawnagent") ||
		strings.Contains(value, "managed worktree") ||
		strings.Contains(value, "managed-worktree") ||
		strings.Contains(value, "isolated worktree") ||
		strings.Contains(value, "子agent") ||
		strings.Contains(value, "子 agent") ||
		strings.Contains(value, "隔离工作树") ||
		strings.Contains(value, "托管工作树")
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
		case compactionEventType:
			var payload compactionPayload
			if json.Unmarshal(event.Payload, &payload) != nil {
				return nil, "", ErrEventIntegrity
			}
			history = append(history, orchestrator.ConversationMessage{Role: "system", Content: payload.Summary, EventID: event.EventID, EventChecksum: event.Checksum, SchemaVersion: 1})
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
				EventID: event.EventID, EventChecksum: event.Checksum,
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
				EventID: event.EventID, EventChecksum: event.Checksum,
				ToolCalls: []orchestrator.ConversationToolCall{{ID: payload.ToolCallID, Name: payload.ToolName, ArgumentsJSON: payload.ArgumentsJSON}},
			})
		case "tool/result":
			var payload toolResultPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, "", fmt.Errorf("%w: invalid tool result at seq %d", ErrEventIntegrity, event.Seq)
			}
			history = append(history, orchestrator.ConversationMessage{
				Role: "tool", Content: payload.Output, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), SchemaVersion: 1,
				EventID: event.EventID, EventChecksum: event.Checksum,
				Name: payload.ToolName, ToolCallID: payload.ToolCallID, IsError: payload.Error != "",
			})
		}
	}
	return history, input, nil
}

func invocationHistoryKey(runID, callID string) string {
	return strings.TrimSpace(runID) + "\x00" + strings.TrimSpace(callID)
}

func (r *SessionRunner) executeTool(ctx context.Context, key runKey, lease runLeasePayload, actor identity.Actor, workingDir string, call orchestrator.ToolCall, retryOfRunIDs ...string) (orchestrator.ToolResult, bool) {
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
	policyAllowed, delegatedBinding, policyErr := agentToolPolicy(events, call.Name)
	if policyErr != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "agent task integrity failure", ExitCode: 1}, true
	}
	bindingChanged := false
	if delegatedBinding != nil {
		current, ok := AgentToolBinding{}, false
		if r.options.AgentToolBinding != nil {
			current, ok = r.options.AgentToolBinding(call.Name)
		}
		if !ok || !validAgentToolBinding(current) || current != *delegatedBinding {
			policyAllowed = false
			bindingChanged = true
		} else {
			call.MCPServer = delegatedBinding.Server
			call.MCPInputSchemaSHA256 = delegatedBinding.InputSchemaSHA256
			call.MCPServerConfigSHA256 = delegatedBinding.ServerConfigSHA256
		}
	}
	if task, _, taskErr := projectAgentTask(events); taskErr != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "agent task integrity failure", ExitCode: 1}, true
	} else if task != nil {
		if _, _, err := r.readAgentTask(ctx, projectionOwner(events), task.Id, key.sessionID); err != nil {
			policyAllowed = false
		}
	}
	state := invocationProjection(events, key.runID, call.ID)
	if (state.prepared != nil && !toolCallMatches(*state.prepared, call)) ||
		(state.dispatched != nil && !toolCallMatches(*state.dispatched, call)) ||
		(state.result != nil && (state.prepared == nil || state.result.ToolName != call.Name)) {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool invocation identity conflict", ExitCode: 1}, true
	}
	if state.result != nil {
		if toolResultSucceeded(*state.result) || len(retryOfRunIDs) == 0 || !canRetryFailedTool(call.Name) {
			return toolResultFromPayload(*state.result), false
		}
		// Retry only read-only or Ledger-idempotent operations. A failed
		// external effect may still have changed state and must not be repeated.
	}
	// A crash can leave commit_pending after tool/call but before
	// tool/dispatched. Treat that marker as an unknown external effect for
	// mutating tools; the checkpoint must never make us execute it twice.
	if !isReadOnlyTool(call.Name) {
		candidateRunIDs := append([]string{key.runID}, retryOfRunIDs...)
		for _, candidateRunID := range candidateRunIDs {
			for _, pending := range pendingCommits(events, candidateRunID) {
				argumentsMatch := strings.TrimSpace(pending.ArgumentsJSON) != "" && pending.ArgumentsJSON == call.ParametersJSON && strings.TrimSpace(pending.ToolName) == call.Name
				if strings.TrimSpace(pending.ToolCallID) != call.ID && !argumentsMatch {
					continue
				}
				_ = r.appendLeasedFact(ctx, key, lease.LeaseID, toolUnknownEventType, toolCallPayload{
					RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON,
				})
				return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool invocation requires reconciliation", ExitCode: 1}, true
			}
		}
	}
	// A retry may replay a tool request whose successful result was durably
	// committed by a failed predecessor. Reuse only that successful receipt and
	// materialize a current-run call/result pair for auditability; never invoke
	// the external tool again. A failed receipt is context for the provider, not
	// an idempotency receipt for read-only operations. Failed external effects
	// retain their result; a nonzero exit code does not prove no effect occurred.
	retryOfRunIDs = normalizedRetryRunIDs("", retryOfRunIDs)
	if len(retryOfRunIDs) > 0 {
		unknownPrior := false
		for _, retryOfRunID := range retryOfRunIDs {
			prior := invocationProjection(events, retryOfRunID, call.ID)
			if (prior.prepared != nil && !toolCallMatches(*prior.prepared, call)) ||
				(prior.dispatched != nil && !toolCallMatches(*prior.dispatched, call)) {
				return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool invocation identity conflict", ExitCode: 1}, true
			}
			if prior.result != nil && (toolResultSucceeded(*prior.result) || !canRetryFailedTool(call.Name)) {
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
			if prior.result != nil {
				// Only a safe failed operation reaches this retry path.
				continue
			}
			if prior.dispatched != nil {
				unknownPrior = true
			}
		}
		if unknownPrior && !isReadOnlyTool(call.Name) {
			_ = r.appendLeasedFact(ctx, key, lease.LeaseID, toolUnknownEventType, toolCallPayload{RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON})
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool invocation requires reconciliation", ExitCode: 1}, true
		}
	}
	if state.dispatched != nil && !isReadOnlyTool(call.Name) {
		_ = r.appendLeasedFact(ctx, key, lease.LeaseID, toolUnknownEventType, toolCallPayload{RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON})
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool invocation requires reconciliation", ExitCode: 1}, true
	}
	callPayload := toolCallPayload{RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON}
	if state.prepared == nil {
		if err := r.appendLeasedSurface(ctx, key, lease, "tool/call", callPayload); err != nil {
			return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool preparation persistence failed", ExitCode: 1}, true
		}
		r.recordProgress(ctx, key, lease, progressPhase, "执行工具", fmt.Sprintf("正在执行 %s。", progressToolName(call.Name)), 0, 0)
	}
	approval := ApprovalDenied
	var approvalErr error
	if policyAllowed {
		approval, approvalErr = r.awaitToolApproval(ctx, key, lease, actor, call)
	}
	if approvalErr != nil {
		unsafe := ctx.Err() == nil
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: approvalErr.Error(), ExitCode: 1}, unsafe
	}
	if approval == ApprovalDenied {
		reason := "tool approval denied"
		if bindingChanged {
			reason = "delegated tool binding changed"
		}
		result := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: reason, ExitCode: 1}
		if err := r.appendLeasedSurface(ctx, key, lease, "tool/result", toolResultPayloadFrom(result, key.runID)); err != nil {
			result.Error = "tool result persistence failed"
			return result, true
		}
		r.recordProgress(ctx, key, lease, progressMilestone, "工具未获批准", "工具执行未获批准，正在根据当前结果继续处理。", 0, 0)
		return result, false
	}
	pending := commitPayload{
		CommitID: commitIDFor(key.runID, call.ID), RunID: key.runID,
		ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON,
	}
	pendingEvent, err := r.appendLeasedFactEvent(ctx, key, lease.LeaseID, commitPendingEventType, pending)
	if err != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool commit preparation failed", ExitCode: 1}, true
	}
	pending.PendingSeq = pendingEvent.Seq
	pending.PendingChecksum = pendingEvent.Checksum
	if err := r.appendLeasedFact(ctx, key, lease.LeaseID, toolDispatchedType, callPayload); err != nil {
		return orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool dispatch persistence failed", ExitCode: 1}, true
	}
	if r.tools == nil && call.Name != "RecallMemory" && call.Name != "ManageMemory" && !agentTool(call.Name) && call.Name != "AskUser" {
		result := orchestrator.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Error: "tool execution is unavailable", ExitCode: 1}
		_ = r.appendLeasedSurface(ctx, key, lease, "tool/result", toolResultPayloadFrom(result, key.runID))
		r.recordProgress(ctx, key, lease, progressMilestone, "工具未完成", "工具执行不可用，正在根据当前结果继续处理。", 0, 0)
		return result, false
	}
	result := orchestrator.ToolResult{}
	if call.Name == "RecallMemory" {
		result = r.recallSessionMemory(ctx, events, call)
	} else if call.Name == "ManageMemory" {
		result = r.manageSessionMemory(ctx, key.sessionID, call)
	} else if agentTool(call.Name) {
		agentCall := call
		agentCall.ID = agentInvocationID(key.runID, retryOfRunIDs, call.ID)
		result = r.ExecuteAgentTool(ctx, actor, key.sessionID, agentCall)
	} else if call.Name == "AskUser" {
		agentCall := call
		agentCall.ID = agentInvocationID(key.runID, retryOfRunIDs, call.ID)
		result = r.askAgentInput(ctx, actor, key.sessionID, agentCall)
	} else if scoped, ok := r.tools.(WorkingDirToolExecutionAdapter); ok {
		result = scoped.ExecuteInWorkingDir(ctx, actor, key.sessionID, workingDir, call)
	} else {
		result = r.tools.Execute(ctx, actor, key.sessionID, call)
	}
	result.ToolCallID = call.ID
	result.ToolName = call.Name
	for _, modification := range codeModificationPayloads(result, key.runID) {
		if err := r.appendLeasedFact(ctx, key, lease.LeaseID, codeModifiedEventType, modification); err != nil {
			_ = r.appendLeasedFact(context.Background(), key, lease.LeaseID, toolUnknownEventType, callPayload)
			result.Error = "code modification receipt persistence failed"
			result.ExitCode = 1
			return result, true
		}
	}
	if err := r.appendLeasedSurface(ctx, key, lease, "tool/result", toolResultPayloadFrom(result, key.runID)); err != nil {
		_ = r.appendLeasedFact(context.Background(), key, lease.LeaseID, toolUnknownEventType, callPayload)
		result.Error = "tool result persistence failed"
		result.ExitCode = 1
		return result, true
	}
	if result.Error != "" || result.ExitCode != 0 {
		r.recordProgress(ctx, key, lease, progressMilestone, "工具未完成", fmt.Sprintf("%s 未完成，正在根据当前结果继续处理。", progressToolName(call.Name)), 0, 0)
	} else {
		r.recordProgress(ctx, key, lease, progressMilestone, "工具完成", fmt.Sprintf("%s 已完成，正在继续下一步。", progressToolName(call.Name)), 0, 0)
	}
	return result, false
}

func isReadOnlyTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "Read", "Glob", "Grep", "RecallMemory":
		return true
	default:
		return false
	}
}

func canRetryFailedTool(name string) bool {
	return isReadOnlyTool(name) || name == "Skill" || name == "SearchKnowledge" || agentTool(name) || name == "AskUser"
}

func (r *SessionRunner) awaitToolApproval(ctx context.Context, key runKey, lease runLeasePayload, actor identity.Actor, call orchestrator.ToolCall) (ApprovalDecision, error) {
	for {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return "", fmt.Errorf("tool approval ledger unavailable: %w", err)
		}
		approval, err := projectToolApproval(events, key.runID, call.ID)
		if err != nil {
			return "", err
		}
		if approval.pending != nil {
			if !toolApprovalMatchesCall(*approval.pending, toolCallPayload{
				RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name, ArgumentsJSON: call.ParametersJSON,
			}) {
				return "", fmt.Errorf("%w: approval identity conflicts with tool call", ErrEventIntegrity)
			}
			if approval.decision != nil {
				return approval.decision.Decision, nil
			}
			task, _, taskErr := projectAgentTask(events)
			if taskErr != nil {
				return "", taskErr
			}
			if task != nil && r.options.AgentApproval != nil {
				pending := agentApprovalView(key.sessionID, *approval.pendingEvent, *approval.pending)
				decision, err := r.options.AgentApproval(ctx, pending)
				if err != nil {
					return "", errors.New("agent approval unavailable")
				}
				_, err = r.workbench.DecideToolApproval(ctx, projectionOwner(events), key.sessionID, ToolApprovalDecisionCommand{
					RunID: pending.RunId, ToolCallID: pending.ToolCallId, PendingEventID: pending.PendingEventId,
					PendingSeq: pending.PendingSeq, Decision: decision,
				})
				if err != nil {
					return "", err
				}
				continue
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}

		decision := permission.Approve
		if r.options.Permissions != nil {
			var arguments map[string]any
			if err := json.Unmarshal([]byte(call.ParametersJSON), &arguments); err != nil {
				return "", fmt.Errorf("invalid tool parameters: %w", err)
			}
			decision = r.options.Permissions.CheckFor(actor, call.Name, arguments)
		}
		switch decision {
		case permission.Approve:
			return ApprovalApproved, nil
		case permission.Deny:
			return ApprovalDenied, nil
		case permission.AskUser:
			pending := toolApprovalPayload{
				RunID: key.runID, ToolCallID: call.ID, ToolName: call.Name,
				ArgumentsJSON: call.ParametersJSON, Decision: ApprovalPending,
			}
			if err := r.appendLeasedFact(ctx, key, lease.LeaseID, approvalPendingEventType, pending); err != nil {
				if errors.Is(err, ErrSequenceConflict) {
					continue
				}
				return "", fmt.Errorf("tool approval persistence failed: %w", err)
			}
			r.recordProgress(ctx, key, lease, progressPhase, "等待审批", "正在等待工具审批后继续执行。", 0, 0)
		default:
			return "", errors.New("unsupported tool permission decision")
		}
	}
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

func commitIDFor(runID, callID string) string {
	return strings.TrimSpace(runID) + "\x00" + strings.TrimSpace(callID)
}

// pendingCommits returns pending side effects which have no matching done
// record. It deliberately treats malformed records as absent here; the
// append-only Ledger integrity check still rejects malformed JSON before a
// runner can use the history.
func pendingCommits(events []Event, runID string) []commitPayload {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	pending := make(map[string]commitPayload)
	for _, event := range events {
		var payload commitPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil || strings.TrimSpace(payload.RunID) != runID {
			continue
		}
		id := strings.TrimSpace(payload.CommitID)
		if id == "" {
			id = commitIDFor(payload.RunID, payload.ToolCallID)
			payload.CommitID = id
		}
		switch event.Type {
		case commitPendingEventType:
			payload.PendingSeq = event.Seq
			payload.PendingChecksum = event.Checksum
			pending[id] = payload
		case commitDoneEventType:
			delete(pending, id)
		}
	}
	result := make([]commitPayload, 0, len(pending))
	for _, payload := range pending {
		result = append(result, payload)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PendingSeq < result[j].PendingSeq })
	return result
}

func toolResultPayloadFrom(result orchestrator.ToolResult, runID string) toolResultPayload {
	return toolResultPayload{RunID: runID, ToolCallID: result.ToolCallID, ToolName: result.ToolName, Output: result.Output, Error: result.Error, ExitCode: result.ExitCode, Truncated: result.Truncated}
}

func toolResultFromPayload(payload toolResultPayload) orchestrator.ToolResult {
	return orchestrator.ToolResult{ToolCallID: payload.ToolCallID, ToolName: payload.ToolName, Output: payload.Output, Error: payload.Error, ExitCode: payload.ExitCode, Truncated: payload.Truncated}
}

func toolResultSucceeded(payload toolResultPayload) bool {
	return strings.TrimSpace(payload.Error) == "" && payload.ExitCode == 0
}

func (r *SessionRunner) appendLeasedSurface(ctx context.Context, key runKey, lease runLeasePayload, eventType string, payload any) error {
	for attempt := 0; attempt < 16; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return err
		}
		view, viewErr := reduceSessionView(events)
		if viewErr != nil {
			return viewErr
		}
		if view.Status == "deleted" {
			return ErrSessionNotFound
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
	_, err := r.appendLeasedFactEvent(ctx, key, leaseID, eventType, payload)
	return err
}

func (r *SessionRunner) appendLeasedFactEvent(ctx context.Context, key runKey, leaseID, eventType string, payload any) (Event, error) {
	for attempt := 0; attempt < 16; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return Event{}, err
		}
		view, viewErr := reduceSessionView(events)
		if viewErr != nil {
			return Event{}, viewErr
		}
		if view.Status == "deleted" {
			return Event{}, ErrSessionNotFound
		}
		projection, err := projectRun(events, key.runID)
		if err != nil {
			return Event{}, err
		}
		if projection.terminal || projection.leaseID != leaseID {
			return Event{}, ErrLeaseLost
		}
		appended, err := r.workbench.ledger.Append(ctx, key.sessionID, int64(len(events)), eventType, payload)
		if err == nil {
			r.workbench.signal(key.sessionID)
			return appended, nil
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return Event{}, err
		}
	}
	return Event{}, ErrSequenceConflict
}

// recordProgress writes a bounded observational receipt. A failure to publish
// that receipt must not change tool, approval, or model execution semantics;
// those durable facts have their own fail-closed write paths.
func (r *SessionRunner) recordProgress(ctx context.Context, key runKey, lease runLeasePayload, kind, title, summary string, planRevision, todoRevision uint64) {
	if r == nil || r.workbench == nil || r.workbench.ledger == nil {
		return
	}
	events, err := r.workbench.ledger.Events(ctx, key.sessionID)
	if err != nil || len(events) == 0 {
		return
	}
	sourceSeq := latestRunSourceSeq(events, key.runID)
	if sourceSeq < 0 {
		return
	}
	payload, err := validateProgressPayload(progressPayload{
		RunID: key.runID, Kind: kind, Title: title, Summary: summary,
		PlanRevision: planRevision, TodoRevision: todoRevision,
		SourceEventSeq: sourceSeq,
	})
	if err != nil {
		return
	}
	_ = r.appendLeasedFact(ctx, key, lease.LeaseID, progressEventType, payload)
}

// recordCompletedProgress appends an observational receipt only after the
// authoritative run-completed fact exists. It intentionally bypasses the
// active-lease writer, which correctly fences all writes after a terminal
// transition, and revalidates the completed run on every CAS retry.
func (r *SessionRunner) recordCompletedProgress(ctx context.Context, key runKey) {
	if r == nil || r.workbench == nil || r.workbench.ledger == nil {
		return
	}
	for attempt := 0; attempt < 16; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil || len(events) == 0 {
			return
		}
		run, err := projectRun(events, key.runID)
		if err != nil || !run.terminal || run.view.Status != RunCompleted {
			return
		}
		sourceSeq := latestRunSourceSeq(events, key.runID)
		if sourceSeq < 0 {
			return
		}
		payload, err := validateProgressPayload(progressPayload{
			RunID: key.runID, Kind: progressMilestone, Title: "任务完成",
			Summary:        "本次任务已完成，已保存回复和执行记录。",
			SourceEventSeq: sourceSeq,
		})
		if err != nil {
			return
		}
		if _, err = r.workbench.ledger.Append(ctx, key.sessionID, int64(len(events)), progressEventType, payload); err == nil {
			r.workbench.signal(key.sessionID)
			return
		}
		if !errors.Is(err, ErrSequenceConflict) {
			return
		}
	}
}

// latestRunSourceSeq returns the newest event whose payload carries the
// current run id. Progress receipts must never point at transport-only facts
// such as agent/worktree-terminal, which intentionally have no run_id.
func latestRunSourceSeq(events []Event, runID string) int64 {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return -1
	}
	for index := len(events) - 1; index >= 0; index-- {
		if eventSourceBelongsToRun(events, events[index].Seq, runID) {
			return events[index].Seq
		}
	}
	return -1
}

func (r *SessionRunner) appendTerminal(ctx context.Context, key runKey, lease runLeasePayload, eventType, publicError string) error {
	// Close the commit protocol before the terminal run fact. A crash before
	// this point leaves commit_pending durable; recovery will fence the same
	// mutating invocation instead of running it twice.
	if err := r.closePendingCommits(ctx, key, lease); err != nil {
		return err
	}
	payload := runTerminalPayload{RunID: key.runID, RequestID: lease.RequestID, LeaseID: lease.LeaseID, Attempt: lease.Attempt, Error: publicError}
	return r.appendLeasedFact(ctx, key, lease.LeaseID, eventType, payload)
}

func (r *SessionRunner) closePendingCommits(ctx context.Context, key runKey, lease runLeasePayload) error {
	for attempt := 0; attempt < 16; attempt++ {
		events, err := r.workbench.ledger.Events(ctx, key.sessionID)
		if err != nil {
			return err
		}
		pending := pendingCommits(events, key.runID)
		if len(pending) == 0 {
			return nil
		}
		for _, item := range pending {
			item.CommitID = strings.TrimSpace(item.CommitID)
			if item.CommitID == "" {
				item.CommitID = commitIDFor(item.RunID, item.ToolCallID)
			}
			if err := r.appendLeasedFact(ctx, key, lease.LeaseID, commitDoneEventType, item); err != nil {
				if errors.Is(err, ErrSequenceConflict) {
					break
				}
				return err
			}
		}
		// Re-read after appending all records; this also handles another
		// writer winning the sequence CAS without producing a duplicate done.
	}
	return ErrSequenceConflict
}

func (r *SessionRunner) fail(key runKey, lease runLeasePayload, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.appendLeasedFact(ctx, key, lease.LeaseID, "execution_result", map[string]any{
		"status":     "run_failed",
		"error_code": publicRunErrorCode(cause),
		"error_type": errorTypeName(cause),
	})
	r.recordProgress(ctx, key, lease, progressMilestone, "运行未完成", "本次运行未完成，可以从当前任务继续或重试。", 0, 0)
	if r.appendTerminal(ctx, key, lease, runFailedEventType, publicRunError(cause)) == nil {
		r.commitSessionMemory(ctx, key)
	}
}

func errorTypeName(cause error) string {
	if cause == nil {
		return "unknown"
	}
	return fmt.Sprintf("%T", cause)
}

func publicRunErrorCode(cause error) string {
	if cause == nil {
		return "unknown"
	}
	message := strings.ToLower(cause.Error())
	switch {
	case errors.Is(cause, ErrEventIntegrity):
		return "event_integrity"
	case strings.Contains(message, "rpc_deadline_exceeded"):
		return "deadline_exceeded"
	case strings.Contains(message, "rpc_unavailable"):
		return "orchestrator_connection_error"
	case strings.Contains(message, "rpc_cancelled"), orchestrator.IsCanceledError(cause):
		return "conversation_canceled"
	case strings.Contains(message, "provider_authentication_error"):
		return "provider_authentication_error"
	case strings.Contains(message, "provider_transport_error"):
		return "provider_transport_error"
	case strings.Contains(message, "provider_timeout"):
		return "provider_timeout"
	case strings.Contains(message, "context_window_exceeded"):
		return "context_window_exceeded"
	case strings.Contains(message, "provider_runtime_error"):
		return "provider_runtime_error"
	case strings.Contains(message, "tool_round_limit"):
		return "tool_round_limit"
	case strings.Contains(message, "empty_model_response"):
		return "empty_model_response"
	case strings.Contains(message, "continuation has no input or history"):
		return "continuation_input_missing"
	case strings.Contains(message, "surface has changed"):
		return "continuation_surface_changed"
	case strings.Contains(message, "foreign") || strings.Contains(message, "unexpected"):
		return "continuation_surface_invalid"
	case orchestrator.IsDeadlineError(cause):
		return "deadline_exceeded"
	case orchestrator.IsConnectionError(cause):
		return "orchestrator_connection_error"
	default:
		return "continuation_runtime_error"
	}
}

// Persist only fixed categories: provider errors can contain credentials or URLs.
func publicRunError(cause error) string {
	if cause == nil {
		return "agent continuation failed"
	}
	message := strings.ToLower(cause.Error())
	switch {
	case errors.Is(cause, orchestrator.ErrCompactionPersistence):
		return "context compaction could not be persisted; task stopped"
	case strings.Contains(message, "rpc_deadline_exceeded"):
		return "model request timed out; retry this task"
	case strings.Contains(message, "rpc_cancelled"), orchestrator.IsCanceledError(cause):
		return "conversation was canceled; retry this task"
	case strings.Contains(message, "rpc_unavailable"):
		return "agent provider connection failed; retry this task"
	case strings.Contains(message, "agent provider connection failed") || strings.Contains(message, "provider_transport_error"):
		return "agent provider connection failed; retry this task"
	case strings.Contains(message, "agent provider failed") || strings.Contains(message, "provider_runtime_error"):
		return "agent provider failed; retry this task"
	case strings.Contains(message, "tool_round_limit"):
		return "tool round limit reached; retry this task"
	case strings.Contains(message, "empty_model_response"):
		return "model returned an empty response; retry this task"
	case strings.Contains(message, "401"), strings.Contains(message, "authentication_error"):
		return "model authentication failed; check provider credentials"
	case strings.Contains(message, "429"):
		return "model rate limit reached; retry later"
	case orchestrator.IsDeadlineError(cause), strings.Contains(message, "timed out"), strings.Contains(message, "timeout"):
		return "model request timed out; retry this task"
	default:
		return "agent continuation failed"
	}
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

// agentInvocationID is the durable idempotency key for mutations owned by an
// Agent tool (task creation, task messages, input requests, and artifacts).
// Ordinary turns are isolated by their current run. A continuation retry must
// keep the oldest retry ancestor as its namespace, otherwise the same logical
// provider call would create a second child task or artifact after a crash.
func agentInvocationID(runID string, retryOfRunIDs []string, callID string) string {
	namespace := strings.TrimSpace(runID)
	if roots := normalizedRetryRunIDs("", retryOfRunIDs); len(roots) > 0 {
		namespace = roots[0]
	}
	return namespace + ":" + strings.TrimSpace(callID)
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
		case runCompletedEventType, runFailedEventType, runCanceledEventType:
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
			} else if event.Type == runCanceledEventType {
				run.view.Status = RunCanceled
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
