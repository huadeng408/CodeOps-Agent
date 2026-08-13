package service

import (
	"context"
	"testing"
)

func TestRerankOutcomeDefaultsFalseAndRecordsOnlyExplicitSuccess(t *testing.T) {
	ctx, outcome := WithRerankOutcome(context.Background())
	if outcome.Applied() {
		t.Fatal("new request outcome must not claim a rerank call")
	}
	MarkRerankApplied(ctx)
	if !outcome.Applied() {
		t.Fatal("explicit successful rerank must be observable to the caller")
	}
}
