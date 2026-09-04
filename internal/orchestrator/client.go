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
	Role      string
	Content   string
	CreatedAt string
}

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
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		// Legacy no-session callers still get an explicit isolated identity. The
		// production CLI always supplies its durable session id.
		sessionID = "ephemeral:" + strings.TrimSpace(c.actor.ActorID)
	}
	actor, err := c.actor.BindSession(sessionID)
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

// IsConnectionError reports whether err looks like a gRPC transport or stream
// failure (a dead orchestrator, broken connection, deadlined RPC) rather than a
// normal orchestrator-level result. The harness uses it to decide whether to
// attempt an orchestrator restart and retry the in-flight turn.
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Internal:
		return true
	}
	// Wrapped errors without a gRPC status (status.Code returns Unknown for
	// those) fall back to substring sniffing so transport failures surfaced as
	// plain errors are still detected.
	msg := strings.ToLower(err.Error())
	for _, hint := range []string{
		"connection", "transport", "rpc error", "stream", "eof",
		"reset", "broken pipe", "unavailable", "no such host", "refused",
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
			Role:      item.Role,
			Content:   item.Content,
			CreatedAt: item.CreatedAt,
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
	if c == nil || c.client == nil {
		return "", errors.New("orchestrator client is nil")
	}

	ctx, cancel := context.WithTimeout(ctx, c.ConversationTimeout())
	defer cancel()

	// Propagate W3C TraceContext per-RPC via gRPC metadata so the Python
	// orchestrator can attach its gen_ai inference spans as children of the
	// Go invoke_agent span — replacing the broken launch-time env-var hack.
	ctx = c.injectTraceMetadata(ctx)
	actor, sessionID, err := c.actorForSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("bind actor to session: %w", err)
	}

	stream, err := c.client.Converse(ctx)
	if err != nil {
		return "", fmt.Errorf("open conversation stream: %w", err)
	}

	historyPayload := make([]*codeagentpb.ConversationMessage, 0, len(history))
	for _, item := range trimConversationHistory(history) {
		historyPayload = append(historyPayload, &codeagentpb.ConversationMessage{
			Role:      item.Role,
			Content:   item.Content,
			CreatedAt: item.CreatedAt,
		})
	}

	if err := stream.Send(&codeagentpb.HarnessMessage{
		Payload: &codeagentpb.HarnessMessage_UserInput{
			UserInput: &codeagentpb.UserInput{
				Text:          input,
				SessionId:     sessionID,
				History:       historyPayload,
				PlanTodoState: state,
				Actor:         actorProto(actor),
			},
		},
	}); err != nil {
		return "", fmt.Errorf("send user input: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()

	var handler ToolHandler
	if len(handlers) > 0 {
		handler = handlers[0]
	}
	var parts []string
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
				if c.OnTextDelta != nil {
					c.OnTextDelta(payload.Text.Text)
				}
			}
		case *codeagentpb.OrchestratorMessage_TodoUpdate:
			if payload.TodoUpdate != nil && c.OnPlanTodoUpdate != nil {
				if err := c.OnPlanTodoUpdate(nil, payload.TodoUpdate); err != nil {
					return "", fmt.Errorf("persist todo update: %w", err)
				}
			}
			if eventHandler != nil {
				eventHandler(ctx, Event{TodoUpdate: payload.TodoUpdate})
			}
		case *codeagentpb.OrchestratorMessage_PlanUpdate:
			if payload.PlanUpdate != nil && c.OnPlanTodoUpdate != nil {
				if err := c.OnPlanTodoUpdate(payload.PlanUpdate, nil); err != nil {
					return "", fmt.Errorf("persist plan update: %w", err)
				}
			}
			if eventHandler != nil {
				eventHandler(ctx, Event{PlanUpdate: payload.PlanUpdate})
			}
		case *codeagentpb.OrchestratorMessage_SessionMeta:
			if eventHandler != nil {
				eventHandler(ctx, Event{SessionMeta: payload.SessionMeta})
			}
		case *codeagentpb.OrchestratorMessage_CompactionUpdate:
			if payload.CompactionUpdate != nil && c.OnCompaction != nil {
				if err := c.OnCompaction(payload.CompactionUpdate); err != nil {
					return "", fmt.Errorf("persist compaction update: %w", err)
				}
			}
		case *codeagentpb.OrchestratorMessage_AgentSpawn:
			if payload.AgentSpawn != nil && c.OnAgentSpawn != nil {
				decision := &codeagentpb.AgentSpawnDecision{RequestId: payload.AgentSpawn.GetRequestId(), Accepted: true}
				if err := c.OnAgentSpawn(ctx, payload.AgentSpawn); err != nil {
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
			}
			if eventHandler != nil {
				eventHandler(ctx, Event{AgentSpawn: payload.AgentSpawn})
			}
		case *codeagentpb.OrchestratorMessage_AgentLifecycle:
			if payload.AgentLifecycle != nil && c.OnAgentLifecycle != nil {
				if err := c.OnAgentLifecycle(ctx, payload.AgentLifecycle); err != nil {
					return "", fmt.Errorf("persist agent lifecycle: %w", err)
				}
			}
		case *codeagentpb.OrchestratorMessage_AskUserRequest:
			if eventHandler != nil {
				eventHandler(ctx, Event{AskUserRequest: payload.AskUserRequest})
			}
			if payload.AskUserRequest == nil {
				continue
			}
			result, err := c.handleAskUser(ctx, payload.AskUserRequest, askHandler)
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
			if err := c.handleToolRequestBatch(ctx, stream, handler, eventHandler, payload.ToolRequestBatch); err != nil {
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
			_, toolSpan := c.startToolSpan(ctx, call)
			emitToolProgress(ctx, eventHandler, call, "start", 1, 1, ToolResult{})
			result := invokeToolHandler(ctx, handler, call)
			finishToolSpan(toolSpan, result)
			emitToolProgress(ctx, eventHandler, call, "finish", 1, 1, result)
			if err := sendToolResult(stream, result); err != nil {
				return "", err
			}
		case *codeagentpb.OrchestratorMessage_Done:
			if payload.Done != nil && payload.Done.Message != "" {
				parts = append(parts, payload.Done.Message)
			}
			return strings.TrimSpace(strings.Join(parts, "")), nil
		}
	}

	return strings.TrimSpace(strings.Join(parts, "")), nil
}

func trimConversationHistory(history []ConversationMessage) []ConversationMessage {
	cleaned := make([]ConversationMessage, 0, len(history))
	for _, item := range history {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if role == "" || content == "" {
			continue
		}
		item.Role = role
		item.Content = truncateHistoryContent(content)
		cleaned = append(cleaned, item)
	}
	if len(cleaned) == 0 {
		return nil
	}

	selected := make([]ConversationMessage, 0, minInt(len(cleaned), maxHistoryMessages))
	usedChars := 0
	omitted := 0
	for idx := len(cleaned) - 1; idx >= 0; idx-- {
		item := cleaned[idx]
		itemChars := len(item.Role) + len(item.Content)
		if len(selected) >= maxHistoryMessages || (len(selected) > 0 && usedChars+itemChars > maxHistoryChars) {
			omitted = idx + 1
			break
		}
		selected = append(selected, item)
		usedChars += itemChars
	}
	reverseConversationMessages(selected)

	if omitted > 0 {
		if len(selected) >= maxHistoryMessages {
			selected = selected[1:]
		}
		summary := ConversationMessage{
			Role:    "system",
			Content: fmt.Sprintf("[History truncated: %d older messages omitted to fit context budget.]", omitted),
		}
		selected = append([]ConversationMessage{summary}, selected...)
	}
	return selected
}

func truncateHistoryContent(content string) string {
	content = strings.TrimSpace(content)
	if len(content) <= maxHistoryMessageChars {
		return content
	}
	return strings.TrimSpace(content[:maxHistoryMessageChars]) + "\n[history message truncated]"
}

func reverseConversationMessages(messages []ConversationMessage) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
	toolSpans := make([]genai.Span, len(requests))
	for i, req := range requests {
		calls[i] = ToolCall{
			ID:                 req.GetToolCallId(),
			Name:               req.GetToolName(),
			ParametersJSON:     req.GetParametersJson(),
			RequiredPermission: req.GetRequiredPermission(),
		}
		_, toolSpans[i] = c.startToolSpan(ctx, calls[i])
		emitToolProgress(ctx, eventHandler, calls[i], "start", i+1, len(requests), ToolResult{})
	}
	if batch.GetParallel() && len(requests) > 1 {
		var wg sync.WaitGroup
		for i, call := range calls {
			wg.Add(1)
			go func(idx int, call ToolCall) {
				defer wg.Done()
				results[idx] = invokeToolHandler(ctx, handler, call)
			}(i, call)
		}
		wg.Wait()
	} else {
		for i, call := range calls {
			results[i] = invokeToolHandler(ctx, handler, call)
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
