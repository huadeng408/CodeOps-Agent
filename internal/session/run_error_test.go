package session

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"code-agent/internal/orchestrator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublicRunErrorNeverIncludesProviderPayload(t *testing.T) {
	for _, tc := range []struct {
		cause error
		want  string
	}{
		{fmt.Errorf("%w: secret-token", orchestrator.ErrCompactionPersistence), "context compaction could not be persisted; task stopped"},
		{errors.New("provider error 401: secret-token"), "model authentication failed; check provider credentials"},
		{errors.New("provider error 429: secret-token"), "model rate limit reached; retry later"},
		{context.DeadlineExceeded, "model request timed out; retry this task"},
		{errors.New("orchestrator conversation failed: agent provider connection failed; retry this task"), "agent provider connection failed; retry this task"},
		{errors.New("orchestrator conversation failed: agent provider failed; retry this task"), "agent provider failed; retry this task"},
		{errors.New("secret-token"), "agent continuation failed"},
		{nil, "agent continuation failed"},
	} {
		if got := publicRunError(tc.cause); got != tc.want {
			t.Fatalf("public error=%q, want %q", got, tc.want)
		}
	}
}

func TestPublicRunErrorPreservesRecoverableTerminalReason(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  string
		want  string
	}{
		{errors.New("orchestrator conversation failed: tool_round_limit: no final model summary"), "tool_round_limit", "tool round limit reached; retry this task"},
		{errors.New("orchestrator conversation failed: empty_model_response"), "empty_model_response", "model returned an empty response; retry this task"},
		{errors.New("rpc_deadline_exceeded: private payload"), "deadline_exceeded", "model request timed out; retry this task"},
		{errors.New("rpc_unavailable: private payload"), "orchestrator_connection_error", "agent provider connection failed; retry this task"},
		{errors.New("rpc_cancelled: private payload"), "conversation_canceled", "conversation was canceled; retry this task"},
	} {
		if got := publicRunErrorCode(tc.cause); got != tc.code {
			t.Fatalf("error code=%q, want %q", got, tc.code)
		}
		if got := publicRunError(tc.cause); got != tc.want {
			t.Fatalf("public error=%q, want %q", got, tc.want)
		}
	}
}

func TestPublicRunErrorClassifiesWrappedGRPCDeadline(t *testing.T) {
	cause := fmt.Errorf("receive orchestrator message: %w", status.Error(codes.DeadlineExceeded, "private provider payload"))
	if got := publicRunErrorCode(cause); got != "deadline_exceeded" {
		t.Fatalf("error code=%q, want deadline_exceeded", got)
	}
	if got := publicRunError(cause); got != "model request timed out; retry this task" {
		t.Fatalf("public error=%q", got)
	}
}
