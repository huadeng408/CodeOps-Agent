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
	"google.golang.org/grpc/credentials/insecure"
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
}

type EventHandler func(context.Context, Event)
type AskUserHandler func(context.Context, *codeagentpb.AskUserRequest) (ToolResult, error)

type Client struct {
	target string
	conn   *grpc.ClientConn
	client codeagentpb.OrchestratorClient
}

func NewClient(target string) (*Client, error) {
	if target == "" {
		target = "127.0.0.1:50051"
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("create orchestrator client: %w", err)
	}

	return &Client{
		target: target,
		conn:   conn,
		client: codeagentpb.NewOrchestratorClient(conn),
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

func (c *Client) Health(ctx context.Context) (*codeagentpb.HealthResponse, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("orchestrator client is nil")
	}

	ctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	return c.client.Health(ctx, &codeagentpb.Empty{})
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

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := c.client.Converse(ctx)
	if err != nil {
		return "", fmt.Errorf("open conversation stream: %w", err)
	}

	historyPayload := make([]*codeagentpb.ConversationMessage, 0, len(history))
	for _, item := range history {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if role == "" || content == "" {
			continue
		}
		historyPayload = append(historyPayload, &codeagentpb.ConversationMessage{
			Role:      role,
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
			if askHandler == nil {
				return "", errors.New("ask user handler is not configured")
			}
			result, err := askHandler(ctx, payload.AskUserRequest)
			if err != nil {
				return "", fmt.Errorf("handle ask user request: %w", err)
			}
			if strings.TrimSpace(result.ToolName) == "" {
				result.ToolName = "AskUser"
			}
			if strings.TrimSpace(result.ToolCallID) == "" {
				result.ToolCallID = payload.AskUserRequest.GetAskUserId()
			}
			if err := sendToolResult(stream, result); err != nil {
				return "", err
			}
		case *codeagentpb.OrchestratorMessage_ToolRequestBatch:
			if payload.ToolRequestBatch == nil {
				continue
			}
			if err := c.handleToolRequestBatch(ctx, stream, handler, payload.ToolRequestBatch); err != nil {
				return "", err
			}
		case *codeagentpb.OrchestratorMessage_ToolRequest:
			if payload.ToolRequest == nil {
				continue
			}
			result := invokeToolHandler(ctx, handler, ToolCall{
				ID:                 payload.ToolRequest.ToolCallId,
				Name:               payload.ToolRequest.ToolName,
				ParametersJSON:     payload.ToolRequest.ParametersJson,
				RequiredPermission: payload.ToolRequest.RequiredPermission,
			})
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

func (c *Client) handleToolRequestBatch(ctx context.Context, stream codeagentpb.Orchestrator_ConverseClient, handler ToolHandler, batch *codeagentpb.ToolRequestBatch) error {
	requests := batch.GetRequests()
	if len(requests) == 0 {
		return nil
	}

	results := make([]ToolResult, len(requests))
	if batch.GetParallel() && len(requests) > 1 {
		var wg sync.WaitGroup
		for i, req := range requests {
			wg.Add(1)
			go func(idx int, request *codeagentpb.ToolRequest) {
				defer wg.Done()
				results[idx] = invokeToolHandler(ctx, handler, ToolCall{
					ID:                 request.GetToolCallId(),
					Name:               request.GetToolName(),
					ParametersJSON:     request.GetParametersJson(),
					RequiredPermission: request.GetRequiredPermission(),
				})
			}(i, req)
		}
		wg.Wait()
	} else {
		for i, req := range requests {
			results[i] = invokeToolHandler(ctx, handler, ToolCall{
				ID:                 req.GetToolCallId(),
				Name:               req.GetToolName(),
				ParametersJSON:     req.GetParametersJson(),
				RequiredPermission: req.GetRequiredPermission(),
			})
		}
	}

	for _, result := range results {
		if err := sendToolResult(stream, result); err != nil {
			return err
		}
	}
	return nil
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
