package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"

	"code-agent/internal/identity"
	"code-agent/internal/telemetry/genai"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ConversationMessage struct {
	Role          string
	Content       string
	CreatedAt     string
	SchemaVersion uint32
	Name          string
	ToolCallID    string
	ToolCalls     []ConversationToolCall
	IsError       bool
	EventID       string
	EventChecksum string
}

type ConversationToolCall struct {
	ID            string
	Name          string
	ArgumentsJSON string
}

// ErrConversationFailed is returned when the orchestrator reaches an explicit
// unsuccessful terminal state. Transport success is not execution success.
var ErrConversationFailed = errors.New("orchestrator conversation failed")

// ActorIdentity is the versioned, non-secret caller identity propagated to
// the Python orchestrator.
type ActorIdentity = identity.Actor

type ToolCall struct {
	ID                 string
	Name               string
	ParametersJSON     string
	RequiredPermission codeagentpb.PermissionLevel
}

type ToolResult struct {
	ToolCallID    string
	ToolName      string
	Output        string
	Error         string
	ExitCode      int32
	Truncated     bool
	Spill         *SpillRef
	ContentBlocks []ContentBlock
	// Changes are Harness-local metadata used to produce content-free ledger
	// receipts. They are not serialized back to the model transport.
	Changes []CodeChange
}

type CodeChange struct {
	Path   string
	Before string
	After  string
}

type SpillRef struct {
	Locator string
	SHA256  string
	Bytes   int64
}

func spillLocator(ref *SpillRef) string {
	if ref == nil {
		return ""
	}
	return ref.Locator
}

func spillSHA256(ref *SpillRef) string {
	if ref == nil {
		return ""
	}
	return ref.SHA256
}

func spillBytes(ref *SpillRef) int64 {
	if ref == nil {
		return 0
	}
	return ref.Bytes
}

type ContentBlock struct {
	Text      string
	ImageBlob []byte
	MIME      string
}

type ToolHandler func(context.Context, ToolCall) ToolResult

type Event struct {
	TodoUpdate     *codeagentpb.TodoUpdate
	PlanUpdate     *codeagentpb.PlanUpdate
	SessionMeta    *codeagentpb.SessionMeta
	AgentSpawn     *codeagentpb.AgentSpawn
	AskUserRequest *codeagentpb.AskUserRequest
	ToolProgress   *ToolProgress
}

type EventHandler func(context.Context, Event)
type AskUserHandler func(context.Context, *codeagentpb.AskUserRequest) (ToolResult, error)
type AgentSpawnHandler func(context.Context, *codeagentpb.AgentSpawn) error
type AgentLifecycleHandler func(context.Context, *codeagentpb.AgentLifecycle) error

// ConversationRequest is the complete request-scoped input for one durable
// orchestrator turn. Actor is intentionally explicit so a shared Client
// connection can safely serve concurrent authenticated Sessions.
type ConversationRequest struct {
	Input     string
	SessionID string
	// WorkingDir is the request-scoped directory selected by the canonical
	// session creation fact. Empty preserves legacy process-default behavior.
	WorkingDir string
	RunID      string
	// RetryOfRunID is the stable root predecessor for a fresh continuation
	// attempt. It remains as a compatibility field for callers that only know
	// one predecessor.
	RetryOfRunID string
	// RetryOfRunIDs contains all verified failed-run predecessors for the same
	// checkpoint. The root is first; the remaining IDs let the Harness reuse a
	// committed tool receipt from any earlier failed attempt.
	RetryOfRunIDs []string
	Resume        bool
	// NewTurn is asserted only for a user message bound to this run's request.
	NewTurn bool
	// SurfaceSHA256 binds a resumed run to the immutable checkpoint Surface.
	// Legal tool/result suffixes may grow while this identity remains stable.
	SurfaceSHA256     string
	Actor             ActorIdentity
	History           []ConversationMessage
	State             *codeagentpb.PlanTodoSnapshot
	HarnessManaged    bool
	MemoryContextJSON string
	AgentTask         *codeagentpb.AgentTask
	AllowedTools      []string
}

type ConversationResult struct {
	Success bool
	Message string
}

// ConversationHandlers contains every callback whose behavior may vary by
// Session. Keeping these callbacks per call prevents concurrent browser runs
// from writing deltas, state, tool results, or Subagent lifecycle facts into
// another Session.
type ConversationHandlers struct {
	Tool           ToolHandler
	Event          EventHandler
	AskUser        AskUserHandler
	TextDelta      func(delta string)
	Compaction     func(update *codeagentpb.CompactionUpdate) error
	PlanTodo       func(plan *codeagentpb.PlanUpdate, todo *codeagentpb.TodoUpdate) error
	AgentSpawn     AgentSpawnHandler
	AgentLifecycle AgentLifecycleHandler
}

type ToolProgress struct {
	ToolCallID string
	ToolName   string
	Phase      string
	Index      int
	Total      int
	ExitCode   int32
	Error      string
	Truncated  bool
}

type Client struct {
	target              string
	conn                *grpc.ClientConn
	client              codeagentpb.OrchestratorClient
	conversationTimeout time.Duration
	askUserTimeout      time.Duration
	actor               identity.Actor
	// OnTextDelta is called for each streaming text chunk from the
	// orchestrator. When nil (default) text deltas are silently
	// accumulated into the final return value.
	OnTextDelta func(delta string)

	// OnCompaction receives a durable surface replacement emitted by the
	// Python orchestrator. Returning an error aborts the current turn so a
	// failed Harness persistence operation cannot be reported as success.
	OnCompaction func(update *codeagentpb.CompactionUpdate) error

	// OnPlanTodoUpdate persists a complete, versioned Plan/Todo projection in
	// the Harness. The callback runs before the public event handler so a stale
	// revision can abort the stream instead of being rendered as committed.
	OnPlanTodoUpdate func(plan *codeagentpb.PlanUpdate, todo *codeagentpb.TodoUpdate) error

	// OnAgentSpawn prepares the Harness-owned isolation boundary before the
	// Python stream resumes and starts the child process.
	OnAgentSpawn AgentSpawnHandler

	// OnAgentLifecycle persists terminal child-worktree state and performs
	// Harness-owned cleanup after a child reports its result.
	OnAgentLifecycle AgentLifecycleHandler

	// Tracer provides gen_ai execute_tool spans; when nil tool spans are skipped.
	tracer genai.Tracer
}

const defaultConversationTimeout = 5 * time.Minute
const defaultAskUserTimeout = 2 * time.Minute
const maxGRPCMessageBytes = 32 << 20
const maxHistoryMessages = 40
const maxHistoryChars = 32_000
const maxHistoryMessageChars = 4_000

func NewClient(target string) (*Client, error) {
	if target == "" {
		target = "127.0.0.1:50051"
	}

	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMessageBytes),
			grpc.MaxCallSendMsgSize(maxGRPCMessageBytes),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create orchestrator client: %w", err)
	}

	return &Client{
		target:              target,
		conn:                conn,
		client:              codeagentpb.NewOrchestratorClient(conn),
		conversationTimeout: defaultConversationTimeout,
		askUserTimeout:      defaultAskUserTimeout,
		actor:               identity.Default(),
	}, nil
}

// SetActor configures the authenticated actor used for subsequent requests.
func (c *Client) SetActor(actor ActorIdentity) error {
	if c == nil {
		return errors.New("orchestrator client is nil")
	}
	actor.Roles = append([]string(nil), actor.Roles...)
	if actor.SchemaVersion == 0 {
		actor.SchemaVersion = identity.SchemaVersion
	}
	if actor.ActorID == "" && actor.Subject == "" && actor.TenantID == "" {
		return errors.New("actor identity is empty")
	}
	if actor.SessionID != "" {
		if err := actor.Validate(); err != nil {
			return err
		}
	}
	c.actor = actor
	return nil
}

// Actor returns a defensive copy of the configured actor identity.
func (c *Client) Actor() ActorIdentity {
	if c == nil {
		return identity.Actor{}
	}
	actor := c.actor
	actor.Roles = append([]string(nil), actor.Roles...)
	return actor
}

func (c *Client) actorForSession(sessionID string) (identity.Actor, string, error) {
	return bindActorToSession(c.Actor(), sessionID)
}

func bindActorToSession(actor identity.Actor, sessionID string) (identity.Actor, string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		// Legacy no-session callers still get an explicit isolated identity. The
		// production CLI always supplies its durable session id.
		sessionID = "ephemeral:" + strings.TrimSpace(actor.ActorID)
	}
	actor, err := actor.BindSession(sessionID)
	if err != nil {
		return identity.Actor{}, "", err
	}
	return actor, sessionID, nil
}

func actorProto(actor identity.Actor) *codeagentpb.ActorContext {
	return &codeagentpb.ActorContext{
		SchemaVersion: actor.SchemaVersion,
		ActorId:       actor.ActorID,
		Subject:       actor.Subject,
		TenantId:      actor.TenantID,
		Roles:         append([]string(nil), actor.Roles...),
		SessionId:     actor.SessionID,
	}
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) Target() string {
	if c == nil {
		return ""
	}
	return c.target
}

func (c *Client) SetConversationTimeout(timeout time.Duration) {
	if c == nil || timeout <= 0 {
		return
	}
	c.conversationTimeout = timeout
}

func (c *Client) ConversationTimeout() time.Duration {
	if c == nil || c.conversationTimeout <= 0 {
		return defaultConversationTimeout
	}
	return c.conversationTimeout
}

func (c *Client) SetAskUserTimeout(timeout time.Duration) {
	if c == nil || timeout <= 0 {
		return
	}
	c.askUserTimeout = timeout
}

func (c *Client) AskUserTimeout() time.Duration {
	if c == nil || c.askUserTimeout <= 0 {
		return defaultAskUserTimeout
	}
	return c.askUserTimeout
}

// SetTracer injects a genai.Tracer for creating execute_tool spans.
// When nil (the default) tool execution is not traced.
func (c *Client) SetTracer(t genai.Tracer) {
	if c == nil {
		return
	}
	c.tracer = t
}

// injectTraceMetadata reads the W3C TraceContext from ctx and injects it
// into gRPC outgoing metadata so the Python orchestrator can parent its
// gen_ai inference spans under the Go invoke_agent span.
//
// Replaces the broken launch-time env-var injection (see process.go) which
// never had a valid span context because the initial process start used
// context.Background().
func (c *Client) injectTraceMetadata(ctx context.Context) context.Context {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return ctx
	}
	if !sc.HasTraceID() || !sc.HasSpanID() {
		return ctx
	}
	tp := fmt.Sprintf("00-%s-%s-%s",
		sc.TraceID().String(),
		sc.SpanID().String(),
		sc.TraceFlags().String(),
	)
	pairs := []string{"traceparent", tp}
	if ts := sc.TraceState().String(); ts != "" {
		pairs = append(pairs, "tracestate", ts)
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}

func (c *Client) Health(ctx context.Context) (*codeagentpb.HealthResponse, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("orchestrator client is nil")
	}

	ctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	return c.client.Health(ctx, &codeagentpb.Empty{})
}

func (c *Client) ReflectMemory(ctx context.Context, request *codeagentpb.MemoryReflectionRequest) (*codeagentpb.MemoryReflectionResponse, error) {
	if c == nil || c.client == nil || request == nil {
		return nil, errors.New("reflection orchestrator unavailable")
	}
	return c.client.ReflectMemory(c.injectTraceMetadata(ctx), request)
}

// ErrCompactionPersistence distinguishes ledger failures from RPC transport failures.
var ErrCompactionPersistence = errors.New("compaction persistence failed")

// IsConnectionError reports whether err looks like a gRPC transport or stream
// failure (a dead orchestrator, broken connection, deadlined RPC) rather than a
// normal orchestrator-level result. The harness uses it to decide whether to
// attempt an orchestrator restart and retry the in-flight turn.
func IsConnectionError(err error) bool {
	if err == nil || errors.Is(err, ErrCompactionPersistence) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if containsProviderAuthFailure(msg) {
		return false
	}

	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	case codes.Internal:
		return containsTransportFailure(msg)
	}
	return containsTransportFailure(msg)
}

func containsProviderAuthFailure(msg string) bool {
	for _, hint := range []string{
		"http 401", "http 403", "unauthorized", "unauthenticated",
		"authentication", "invalid api key", "invalid_api_key",
		"permission denied", "access denied", "forbidden",
	} {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

func containsTransportFailure(msg string) bool {
	for _, hint := range []string{
		"connection refused", "connection reset", "connection closed",
		"transport is closing", "transport: closing", "desc = transport", "broken pipe",
		"no such host", "network is unreachable", "dial tcp", "tls handshake timeout",
		"unexpected eof", "unexpected end of file", "read: eof", "write: eof",
	} {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

func (c *Client) Converse(ctx context.Context, input string, handlers ...ToolHandler) (string, error) {
	return c.ConverseWithEvents(ctx, input, nil, handlers...)
}

// Compact asks the Python orchestrator to compact persisted history without
// starting a model conversation. A non-empty update is forwarded through the
// same durable Harness callback used by automatic compaction.
func (c *Client) Compact(ctx context.Context, sessionID string, history []ConversationMessage) (*codeagentpb.CompactionUpdate, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("orchestrator client is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, c.ConversationTimeout())
	defer cancel()
	ctx = c.injectTraceMetadata(ctx)
	actor, sessionID, err := c.actorForSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("bind actor to session: %w", err)
	}

	historyPayload := make([]*codeagentpb.ConversationMessage, 0, len(history))
	for _, item := range trimConversationHistory(history) {
		historyPayload = append(historyPayload, &codeagentpb.ConversationMessage{
			Role: item.Role, Content: item.Content, CreatedAt: item.CreatedAt,
			SchemaVersion: item.SchemaVersion, Name: item.Name, ToolCallId: item.ToolCallID, IsError: item.IsError,
			EventId: item.EventID, EventChecksum: item.EventChecksum,
			ToolCalls: conversationToolCalls(item.ToolCalls),
		})
	}
	update, err := c.client.Compact(ctx, &codeagentpb.CompactRequest{
		SessionId: sessionID,
		History:   historyPayload,
		Actor:     actorProto(actor),
	})
	if err != nil {
		return nil, fmt.Errorf("compact orchestrator history: %w", err)
	}
	if update != nil && strings.TrimSpace(update.GetSummary()) != "" && c.OnCompaction != nil {
		if err := c.OnCompaction(update); err != nil {
			return nil, fmt.Errorf("persist compaction update: %w", err)
		}
	}
	return update, nil
}

func (c *Client) ConverseWithHistory(ctx context.Context, input string, sessionID string, history []ConversationMessage, handlers ...ToolHandler) (string, error) {
	return c.ConverseWithHistoryAndPrompts(ctx, input, sessionID, history, nil, nil, handlers...)
}

func (c *Client) ConverseWithEvents(ctx context.Context, input string, eventHandler EventHandler, handlers ...ToolHandler) (string, error) {
	return c.ConverseWithPrompts(ctx, input, eventHandler, nil, handlers...)
}

func (c *Client) ConverseWithPrompts(ctx context.Context, input string, eventHandler EventHandler, askHandler AskUserHandler, handlers ...ToolHandler) (string, error) {
	return c.ConverseWithHistoryAndPrompts(ctx, input, "", nil, eventHandler, askHandler, handlers...)
}

func (c *Client) ConverseWithHistoryAndPrompts(ctx context.Context, input string, sessionID string, history []ConversationMessage, eventHandler EventHandler, askHandler AskUserHandler, handlers ...ToolHandler) (string, error) {
	return c.ConverseWithHistoryAndState(ctx, input, sessionID, history, nil, eventHandler, askHandler, handlers...)
}

// ConverseWithHistoryAndState sends a request together with the Harness's
// current Plan/Todo snapshot. Keeping this as an additive API preserves the
// existing callers while making state recovery explicit at the protocol edge.
func (c *Client) ConverseWithHistoryAndState(ctx context.Context, input string, sessionID string, history []ConversationMessage, state *codeagentpb.PlanTodoSnapshot, eventHandler EventHandler, askHandler AskUserHandler, handlers ...ToolHandler) (string, error) {
	if c == nil {
		return "", errors.New("orchestrator client is nil")
	}
	var toolHandler ToolHandler
	if len(handlers) > 0 {
		toolHandler = handlers[0]
	}
	result, err := c.RunConversation(ctx, ConversationRequest{
		Input:     input,
		SessionID: sessionID,
		History:   history,
		State:     state,
		Actor:     c.Actor(),
	}, ConversationHandlers{
		TextDelta:      c.OnTextDelta,
		Compaction:     c.OnCompaction,
		PlanTodo:       c.OnPlanTodoUpdate,
		AgentSpawn:     c.OnAgentSpawn,
		AgentLifecycle: c.OnAgentLifecycle,
		Event:          eventHandler,
		AskUser:        askHandler,
		Tool:           toolHandler,
	})
	return result.Message, err
}

// RunConversation runs one request without reading any actor or callback from
// shared Client state. The Client owns only connection-level configuration;
// all Session-varying behavior crosses this interface explicitly.
func (c *Client) RunConversation(ctx context.Context, request ConversationRequest, handlers ConversationHandlers) (ConversationResult, error) {
	message, err := c.runConversation(ctx, request, handlers)
	if err != nil {
		return ConversationResult{Success: false}, err
	}
	return ConversationResult{Success: true, Message: message}, nil
}

func (c *Client) runConversation(ctx context.Context, request ConversationRequest, handlers ConversationHandlers) (string, error) {
	if c == nil || c.client == nil {
		return "", errors.New("orchestrator client is nil")
	}

	ctx, cancel := context.WithTimeout(ctx, c.ConversationTimeout())
	defer cancel()

	// Propagate W3C TraceContext per-RPC via gRPC metadata so the Python
	// orchestrator can attach its gen_ai inference spans as children of the
	// Go invoke_agent span — replacing the broken launch-time env-var hack.
	ctx = c.injectTraceMetadata(ctx)
	runID := strings.TrimSpace(request.RunID)
	if request.Resume && runID == "" {
		return "", errors.New("resume conversation run id is required")
	}
	if runID != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-code-agent-run-id", runID)
	}
	if request.Resume {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-code-agent-resume", "true")
		if request.NewTurn {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-code-agent-new-turn", "true")
		}
		surfaceSHA256 := strings.TrimSpace(request.SurfaceSHA256)
		if surfaceSHA256 != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-code-agent-surface-sha256", surfaceSHA256)
		}
		retryOfIDs := normalizedRetryRunIDs(request.RetryOfRunID, request.RetryOfRunIDs)
		if len(retryOfIDs) > 0 {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-code-agent-retry-of-run-id", retryOfIDs[0])
			ctx = metadata.AppendToOutgoingContext(ctx, "x-code-agent-retry-of-run-ids", strings.Join(retryOfIDs, ","))
		}
	}
	actor, sessionID, err := bindActorToSession(request.Actor, request.SessionID)
	if err != nil {
		return "", fmt.Errorf("bind actor to session: %w", err)
	}

	stream, err := c.client.Converse(ctx)
	if err != nil {
		return "", fmt.Errorf("open conversation stream: %w", err)
	}

	historyPayload := make([]*codeagentpb.ConversationMessage, 0, len(request.History))
	for _, item := range conversationRequestHistory(request) {
		historyPayload = append(historyPayload, &codeagentpb.ConversationMessage{
			Role: item.Role, Content: item.Content, CreatedAt: item.CreatedAt,
			SchemaVersion: item.SchemaVersion, Name: item.Name, ToolCallId: item.ToolCallID, IsError: item.IsError,
			EventId: item.EventID, EventChecksum: item.EventChecksum,
			ToolCalls: conversationToolCalls(item.ToolCalls),
		})
	}

	if err := stream.Send(&codeagentpb.HarnessMessage{
		Payload: &codeagentpb.HarnessMessage_UserInput{
			UserInput: &codeagentpb.UserInput{
				Text:              request.Input,
				SessionId:         sessionID,
				WorkingDir:        request.WorkingDir,
				History:           historyPayload,
				PlanTodoState:     request.State,
				Actor:             actorProto(actor),
				HarnessManaged:    request.HarnessManaged,
				MemoryContextJson: request.MemoryContextJSON,
				AgentTask:         request.AgentTask,
				AllowedTools:      request.AllowedTools,
			},
		},
	}); err != nil {
		return "", fmt.Errorf("send user input: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()

	var parts []string
	doneReceived := false
	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("receive orchestrator message: %w", err)
		}

		switch payload := msg.Payload.(type) {
		case *codeagentpb.OrchestratorMessage_Text:
			if payload.Text != nil {
				parts = append(parts, payload.Text.Text)
				if handlers.TextDelta != nil {
					handlers.TextDelta(payload.Text.Text)
				}
			}
		case *codeagentpb.OrchestratorMessage_TodoUpdate:
			if payload.TodoUpdate != nil && handlers.PlanTodo != nil {
				if err := handlers.PlanTodo(nil, payload.TodoUpdate); err != nil {
					return "", fmt.Errorf("persist todo update: %w", err)
				}
			}
			if handlers.Event != nil {
				handlers.Event(ctx, Event{TodoUpdate: payload.TodoUpdate})
			}
		case *codeagentpb.OrchestratorMessage_PlanUpdate:
			if payload.PlanUpdate != nil && handlers.PlanTodo != nil {
				if err := handlers.PlanTodo(payload.PlanUpdate, nil); err != nil {
					return "", fmt.Errorf("persist plan update: %w", err)
				}
			}
			if handlers.Event != nil {
				handlers.Event(ctx, Event{PlanUpdate: payload.PlanUpdate})
			}
		case *codeagentpb.OrchestratorMessage_SessionMeta:
			if handlers.Event != nil {
				handlers.Event(ctx, Event{SessionMeta: payload.SessionMeta})
			}
		case *codeagentpb.OrchestratorMessage_CompactionUpdate:
			if payload.CompactionUpdate != nil {
				if handlers.Compaction == nil {
					return "", fmt.Errorf("%w: handler is not configured", ErrCompactionPersistence)
				}
				if err := handlers.Compaction(payload.CompactionUpdate); err != nil {
					return "", fmt.Errorf("%w: %w", ErrCompactionPersistence, err)
				}
			}
		case *codeagentpb.OrchestratorMessage_AgentSpawn:
			if payload.AgentSpawn != nil && handlers.AgentSpawn != nil {
				decision := &codeagentpb.AgentSpawnDecision{RequestId: payload.AgentSpawn.GetRequestId(), Accepted: true}
				if err := handlers.AgentSpawn(ctx, payload.AgentSpawn); err != nil {
					decision.Accepted = false
					decision.Error = "harness rejected agent worktree"
					_ = stream.Send(&codeagentpb.HarnessMessage{Payload: &codeagentpb.HarnessMessage_AgentSpawnDecision{AgentSpawnDecision: decision}})
					return "", fmt.Errorf("prepare agent worktree: %w", err)
				}
				// Legacy orchestrators may close their request side immediately after
				// emitting AgentSpawn. The production runner waits for this decision;
				// a best-effort send keeps older servers readable while any real
				// transport failure is still observed on the following Recv.
				_ = stream.Send(&codeagentpb.HarnessMessage{Payload: &codeagentpb.HarnessMessage_AgentSpawnDecision{AgentSpawnDecision: decision}})
			} else if payload.AgentSpawn != nil && request.Resume {
				// A continuation run must never let an agent spawn request disappear
				// silently. Without a Harness worktree/permission handler, reject it
				// explicitly so the Python side can terminate and the run is failed.
				decision := &codeagentpb.AgentSpawnDecision{
					RequestId: payload.AgentSpawn.GetRequestId(), Accepted: false,
					Error: "agent worktree handler unavailable",
				}
				_ = stream.Send(&codeagentpb.HarnessMessage{Payload: &codeagentpb.HarnessMessage_AgentSpawnDecision{AgentSpawnDecision: decision}})
				return "", errors.New("agent spawn rejected: Harness worktree handler unavailable")
			}
			if handlers.Event != nil {
				handlers.Event(ctx, Event{AgentSpawn: payload.AgentSpawn})
			}
		case *codeagentpb.OrchestratorMessage_AgentLifecycle:
			if payload.AgentLifecycle != nil && handlers.AgentLifecycle != nil {
				if err := handlers.AgentLifecycle(ctx, payload.AgentLifecycle); err != nil {
					return "", fmt.Errorf("persist agent lifecycle: %w", err)
				}
			}
		case *codeagentpb.OrchestratorMessage_AskUserRequest:
			if handlers.Event != nil {
				handlers.Event(ctx, Event{AskUserRequest: payload.AskUserRequest})
			}
			if payload.AskUserRequest == nil {
				continue
			}
			result, err := c.handleAskUser(ctx, payload.AskUserRequest, handlers.AskUser)
			if err != nil {
				return "", fmt.Errorf("handle ask user request: %w", err)
			}
			if err := sendToolResult(stream, result); err != nil {
				return "", err
			}
		case *codeagentpb.OrchestratorMessage_ToolRequestBatch:
			if payload.ToolRequestBatch == nil {
				continue
			}
			if err := c.handleToolRequestBatch(ctx, stream, handlers.Tool, handlers.Event, payload.ToolRequestBatch); err != nil {
				return "", err
			}
		case *codeagentpb.OrchestratorMessage_ToolRequest:
			if payload.ToolRequest == nil {
				continue
			}
			call := ToolCall{
				ID:                 payload.ToolRequest.ToolCallId,
				Name:               payload.ToolRequest.ToolName,
				ParametersJSON:     payload.ToolRequest.ParametersJson,
				RequiredPermission: payload.ToolRequest.RequiredPermission,
			}
			toolCtx, toolSpan := c.startToolSpan(ctx, call)
			emitToolProgress(ctx, handlers.Event, call, "start", 1, 1, ToolResult{})
			result := invokeToolHandler(toolCtx, handlers.Tool, call)
			finishToolSpan(toolSpan, result)
			emitToolProgress(ctx, handlers.Event, call, "finish", 1, 1, result)
			if err := sendToolResult(stream, result); err != nil {
				return "", err
			}
		case *codeagentpb.OrchestratorMessage_Done:
			if payload.Done == nil {
				continue
			}
			if !payload.Done.GetSuccess() {
				reason := strings.TrimSpace(payload.Done.GetMessage())
				if reason == "" {
					reason = "unspecified failure"
				}
				if code := strings.TrimSpace(payload.Done.GetErrorCode()); code != "" {
					reason = code + ": " + reason
				}
				return "", fmt.Errorf("%w: %s", ErrConversationFailed, reason)
			}
			if payload.Done.Message != "" {
				parts = append(parts, payload.Done.Message)
			}
			doneReceived = true
			return strings.TrimSpace(strings.Join(parts, "")), nil
		}
	}

	if !doneReceived {
		return "", fmt.Errorf("%w: orchestrator stream ended before terminal done", ErrConversationFailed)
	}
	return strings.TrimSpace(strings.Join(parts, "")), nil
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

func conversationToolCalls(calls []ConversationToolCall) []*codeagentpb.ConversationToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]*codeagentpb.ConversationToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, &codeagentpb.ConversationToolCall{Id: call.ID, Name: call.Name, ArgumentsJson: call.ArgumentsJSON})
	}
	return out
}

func trimConversationHistory(history []ConversationMessage) []ConversationMessage {
	cleaned := make([]ConversationMessage, 0, len(history))
	for _, item := range history {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if role == "" || (content == "" && len(item.ToolCalls) == 0 && strings.TrimSpace(item.ToolCallID) == "") {
			continue
		}
		item.Role = role
		if content != "" {
			item.Content = truncateHistoryContent(content)
		} else {
			item.Content = ""
		}
		cleaned = append(cleaned, item)
	}
	if len(cleaned) == 0 {
		return nil
	}

	groups := conversationHistoryGroups(cleaned)
	selectedGroups := make([][]ConversationMessage, 0, len(groups))
	selectedCount := 0
	usedChars := 0
	omitted := 0
	for idx := len(groups) - 1; idx >= 0; idx-- {
		group := groups[idx]
		groupChars := conversationHistoryGroupChars(group)
		if selectedCount+len(group) > maxHistoryMessages || (selectedCount > 0 && usedChars+groupChars > maxHistoryChars) {
			for _, older := range groups[:idx+1] {
				omitted += len(older)
			}
			break
		}
		selectedGroups = append(selectedGroups, group)
		selectedCount += len(group)
		usedChars += groupChars
	}
	reverseConversationMessageGroups(selectedGroups)

	if omitted > 0 {
		// Reserve one slot for the truncation marker. Remove whole causal groups
		// so an assistant tool call is never separated from its tool results.
		for selectedCount >= maxHistoryMessages && len(selectedGroups) > 0 {
			omitted += len(selectedGroups[0])
			selectedCount -= len(selectedGroups[0])
			selectedGroups = selectedGroups[1:]
		}
		selectedGroups = append([][]ConversationMessage{{{
			Role:    "system",
			Content: fmt.Sprintf("[History truncated: %d older messages omitted to fit context budget.]", omitted),
		}}}, selectedGroups...)
	}
	selected := make([]ConversationMessage, 0, selectedCount+1)
	for _, group := range selectedGroups {
		selected = append(selected, group...)
	}
	return selected
}

// conversationHistoryGroups keeps each assistant tool-call record together
// with the contiguous tool results that answer it. The group is the smallest
// causal unit that history trimming may retain or omit.
func conversationHistoryGroups(history []ConversationMessage) [][]ConversationMessage {
	groups := make([][]ConversationMessage, 0, len(history))
	for index := 0; index < len(history); {
		item := history[index]
		group := []ConversationMessage{item}
		index++
		if item.Role == "assistant" && len(item.ToolCalls) > 0 {
			for index < len(history) && history[index].Role == "tool" {
				group = append(group, history[index])
				index++
			}
		}
		groups = append(groups, group)
	}
	return groups
}

func conversationHistoryGroupChars(group []ConversationMessage) int {
	total := 0
	for _, item := range group {
		total += len(item.Role) + len(item.Content) + len(item.Name) + len(item.ToolCallID)
		for _, call := range item.ToolCalls {
			total += len(call.ID) + len(call.Name) + len(call.ArgumentsJSON)
		}
	}
	return total
}

func conversationRequestHistory(request ConversationRequest) []ConversationMessage {
	// Canonical history is budgeted by Python's context manager, not transport.
	if request.Resume {
		return request.History
	}
	return trimConversationHistory(request.History)
}

func truncateHistoryContent(content string) string {
	content = strings.TrimSpace(content)
	if len(content) <= maxHistoryMessageChars {
		return content
	}
	end := maxHistoryMessageChars
	for end > 0 && content[end]&0xc0 == 0x80 {
		end--
	}
	return strings.TrimSpace(content[:end]) + "\n[history message truncated]"
}

func reverseConversationMessageGroups(groups [][]ConversationMessage) {
	for left, right := 0, len(groups)-1; left < right; left, right = left+1, right-1 {
		groups[left], groups[right] = groups[right], groups[left]
	}
}

// startToolSpan creates an execute_tool span as a child of the context's
// current span. Returns the span (or nil when tracing is disabled).
func (c *Client) startToolSpan(ctx context.Context, call ToolCall) (context.Context, genai.Span) {
	if c == nil || c.tracer == nil {
		return ctx, nil
	}
	ctx, sp := c.tracer.StartSpan(ctx, "execute_tool "+call.Name, genai.OperationExecuteTool, genai.SystemGenAI)
	sp.SetAttributes(genai.ToolNameKV(call.Name), genai.ToolCallIDKV(call.ID))
	return ctx, sp
}

// finishToolSpan sets the result attribute and ends the span.
func finishToolSpan(sp genai.Span, result ToolResult) {
	if sp == nil {
		return
	}
	sp.SetAttributes(genai.ToolCallResultKV(result.Output))
	if result.Error != "" || result.ExitCode != 0 {
		sp.RecordError(fmt.Errorf("%s (exit=%d)", result.Error, result.ExitCode))
	}
	sp.End()
}

func (c *Client) handleToolRequestBatch(ctx context.Context, stream codeagentpb.Orchestrator_ConverseClient, handler ToolHandler, eventHandler EventHandler, batch *codeagentpb.ToolRequestBatch) error {
	requests := batch.GetRequests()
	if len(requests) == 0 {
		return nil
	}

	results := make([]ToolResult, len(requests))
	calls := make([]ToolCall, len(requests))
	toolContexts := make([]context.Context, len(requests))
	toolSpans := make([]genai.Span, len(requests))
	for i, req := range requests {
		calls[i] = ToolCall{
			ID:                 req.GetToolCallId(),
			Name:               req.GetToolName(),
			ParametersJSON:     req.GetParametersJson(),
			RequiredPermission: req.GetRequiredPermission(),
		}
		toolContexts[i], toolSpans[i] = c.startToolSpan(ctx, calls[i])
		emitToolProgress(ctx, eventHandler, calls[i], "start", i+1, len(requests), ToolResult{})
	}
	if batch.GetParallel() && len(requests) > 1 {
		var wg sync.WaitGroup
		for i, call := range calls {
			wg.Add(1)
			go func(idx int, call ToolCall) {
				defer wg.Done()
				results[idx] = invokeToolHandler(toolContexts[idx], handler, call)
			}(i, call)
		}
		wg.Wait()
	} else {
		for i, call := range calls {
			results[i] = invokeToolHandler(toolContexts[i], handler, call)
		}
	}

	for i, result := range results {
		finishToolSpan(toolSpans[i], result)
		emitToolProgress(ctx, eventHandler, calls[i], "finish", i+1, len(requests), result)
		if err := sendToolResult(stream, result); err != nil {
			return err
		}
	}
	return nil
}

func emitToolProgress(ctx context.Context, handler EventHandler, call ToolCall, phase string, index, total int, result ToolResult) {
	if handler == nil {
		return
	}
	handler(ctx, Event{
		ToolProgress: &ToolProgress{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Phase:      phase,
			Index:      index,
			Total:      total,
			ExitCode:   result.ExitCode,
			Error:      result.Error,
			Truncated:  result.Truncated,
		},
	})
}

func (c *Client) handleAskUser(ctx context.Context, request *codeagentpb.AskUserRequest, handler AskUserHandler) (ToolResult, error) {
	if request == nil {
		return ToolResult{}, errors.New("ask user request is nil")
	}
	if handler == nil {
		return defaultAskUserResult(request.GetAskUserId(), "ask user handler is not configured"), nil
	}

	askCtx, cancel := context.WithTimeout(ctx, c.AskUserTimeout())
	defer cancel()

	type askOutcome struct {
		result ToolResult
		err    error
	}
	done := make(chan askOutcome, 1)
	go func() {
		result, err := handler(askCtx, request)
		done <- askOutcome{result: result, err: err}
	}()

	select {
	case outcome := <-done:
		if outcome.err != nil {
			return ToolResult{}, outcome.err
		}
		result := outcome.result
		if strings.TrimSpace(result.ToolName) == "" {
			result.ToolName = "AskUser"
		}
		if strings.TrimSpace(result.ToolCallID) == "" {
			result.ToolCallID = request.GetAskUserId()
		}
		return result, nil
	case <-askCtx.Done():
		if ctx.Err() != nil {
			return ToolResult{}, ctx.Err()
		}
		return defaultAskUserResult(
			request.GetAskUserId(),
			fmt.Sprintf("ask user timed out after %s", c.AskUserTimeout()),
		), nil
	}
}

func defaultAskUserResult(callID, reason string) ToolResult {
	return ToolResult{
		ToolCallID: callID,
		ToolName:   "AskUser",
		Error:      reason,
		ExitCode:   1,
	}
}

func invokeToolHandler(ctx context.Context, handler ToolHandler, call ToolCall) ToolResult {
	result := ToolResult{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Error:      "no tool handler configured",
		ExitCode:   1,
	}
	if handler != nil {
		result = handler(ctx, call)
	}
	if strings.TrimSpace(result.ToolName) == "" {
		result.ToolName = call.Name
	}
	if strings.TrimSpace(result.ToolCallID) == "" {
		result.ToolCallID = call.ID
	}
	return result
}

func sendToolResult(stream codeagentpb.Orchestrator_ConverseClient, result ToolResult) error {
	if result.ToolName == "" {
		return fmt.Errorf("send tool result: missing tool name")
	}
	contentBlocks := make([]*codeagentpb.ContentBlock, 0, len(result.ContentBlocks))
	for _, block := range result.ContentBlocks {
		contentBlocks = append(contentBlocks, &codeagentpb.ContentBlock{
			Text:      block.Text,
			ImageBlob: block.ImageBlob,
			Mime:      block.MIME,
		})
	}
	if err := stream.Send(&codeagentpb.HarnessMessage{
		Payload: &codeagentpb.HarnessMessage_ToolResult{
			ToolResult: &codeagentpb.ToolResult{
				ToolName:      result.ToolName,
				Output:        result.Output,
				Error:         result.Error,
				ExitCode:      result.ExitCode,
				Truncated:     result.Truncated,
				ToolCallId:    result.ToolCallID,
				ContentBlocks: contentBlocks,
				SpillLocator:  spillLocator(result.Spill),
				SpillSha256:   spillSHA256(result.Spill),
				SpillBytes:    spillBytes(result.Spill),
			},
		},
	}); err != nil {
		return fmt.Errorf("send tool result: %w", err)
	}
	return nil
}
