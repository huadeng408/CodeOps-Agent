package session

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"code-agent/internal/orchestrator"
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
