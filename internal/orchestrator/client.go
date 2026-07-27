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

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type ConversationMessage struct {
	Role      string
	Content   string
	CreatedAt string
}

type ToolCall struct {
	ID                 string
	Name               string
	ParametersJSON     string
	RequiredPermission codeagentpb.PermissionLevel
}

type ToolResult struct {
	ToolCallID string
	ToolName   string
	Output     string
	Error      string
	ExitCode   int32
	Truncated  bool
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
	// OnTextDelta is called for each streaming text chunk from the
	// orchestrator. When nil (default) text deltas are silently
	// accumulated into the final return value.
	OnTextDelta func(delta string)
}

const defaultConversationTimeout = 5 * time.Minute
const defaultAskUserTimeout = 2 * time.Minute
const maxHistoryMessages = 40
const maxHistoryChars = 32_000
const maxHistoryMessageChars = 4_000

func NewClient(target string) (*Client, error) {
	if target == "" {
		target = "127.0.0.1:50051"
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("create orchestrator client: %w", err)
	}

	return &Client{
		target:              target,
		conn:                conn,
		client:              codeagentpb.NewOrchestratorClient(conn),
		conversationTimeout: defaultConversationTimeout,
		askUserTimeout:      defaultAskUserTimeout,
	}, nil
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
	if c == nil || c.client == nil {
		return "", errors.New("orchestrator client is nil")
	}

	ctx, cancel := context.WithTimeout(ctx, c.ConversationTimeout())
	defer cancel()

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
				Text:      input,
				SessionId: strings.TrimSpace(sessionID),
				History:   historyPayload,
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
			if eventHandler != nil {
				eventHandler(ctx, Event{TodoUpdate: payload.TodoUpdate})
			}
		case *codeagentpb.OrchestratorMessage_PlanUpdate:
			if eventHandler != nil {
				eventHandler(ctx, Event{PlanUpdate: payload.PlanUpdate})
			}
		case *codeagentpb.OrchestratorMessage_SessionMeta:
			if eventHandler != nil {
				eventHandler(ctx, Event{SessionMeta: payload.SessionMeta})
			}
		case *codeagentpb.OrchestratorMessage_AgentSpawn:
			if eventHandler != nil {
				eventHandler(ctx, Event{AgentSpawn: payload.AgentSpawn})
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
			emitToolProgress(ctx, eventHandler, call, "start", 1, 1, ToolResult{})
			result := invokeToolHandler(ctx, handler, call)
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

func (c *Client) handleToolRequestBatch(ctx context.Context, stream codeagentpb.Orchestrator_ConverseClient, handler ToolHandler, eventHandler EventHandler, batch *codeagentpb.ToolRequestBatch) error {
	requests := batch.GetRequests()
	if len(requests) == 0 {
		return nil
	}

	results := make([]ToolResult, len(requests))
	calls := make([]ToolCall, len(requests))
	for i, req := range requests {
		calls[i] = ToolCall{
			ID:                 req.GetToolCallId(),
			Name:               req.GetToolName(),
			ParametersJSON:     req.GetParametersJson(),
			RequiredPermission: req.GetRequiredPermission(),
		}
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
	if err := stream.Send(&codeagentpb.HarnessMessage{
		Payload: &codeagentpb.HarnessMessage_ToolResult{
			ToolResult: &codeagentpb.ToolResult{
				ToolName:   result.ToolName,
				Output:     result.Output,
				Error:      result.Error,
				ExitCode:   result.ExitCode,
				Truncated:  result.Truncated,
				ToolCallId: result.ToolCallID,
			},
		},
	}); err != nil {
		return fmt.Errorf("send tool result: %w", err)
	}
	return nil
}
