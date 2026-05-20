package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	codeagentpb "code-agent/gen/codeagentpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type ToolCall struct {
	Name               string
	ParametersJSON     string
	RequiredPermission codeagentpb.PermissionLevel
}

type ToolResult struct {
	ToolName  string
	Output    string
	Error     string
	ExitCode  int32
	Truncated bool
}

type ToolHandler func(context.Context, ToolCall) ToolResult

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
	if c == nil || c.client == nil {
		return "", errors.New("orchestrator client is nil")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := c.client.Converse(ctx)
	if err != nil {
		return "", fmt.Errorf("open conversation stream: %w", err)
	}

	if err := stream.Send(&codeagentpb.HarnessMessage{
		Payload: &codeagentpb.HarnessMessage_UserInput{
			UserInput: &codeagentpb.UserInput{Text: input},
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
		case *codeagentpb.OrchestratorMessage_ToolRequest:
			if payload.ToolRequest == nil {
				continue
			}
			result := ToolResult{
				ToolName: payload.ToolRequest.ToolName,
				Error:    "no tool handler configured",
				ExitCode: 1,
			}
			if handler != nil {
				result = handler(ctx, ToolCall{
					Name:               payload.ToolRequest.ToolName,
					ParametersJSON:     payload.ToolRequest.ParametersJson,
					RequiredPermission: payload.ToolRequest.RequiredPermission,
				})
			}
			if err := stream.Send(&codeagentpb.HarnessMessage{
				Payload: &codeagentpb.HarnessMessage_ToolResult{
					ToolResult: &codeagentpb.ToolResult{
						ToolName:  result.ToolName,
						Output:    result.Output,
						Error:     result.Error,
						ExitCode:  result.ExitCode,
						Truncated: result.Truncated,
					},
				},
			}); err != nil {
				return "", fmt.Errorf("send tool result: %w", err)
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
