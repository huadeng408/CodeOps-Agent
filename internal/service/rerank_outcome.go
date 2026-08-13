package service

import "context"

type rerankOutcomeKey struct{}

// RerankOutcome records whether this request completed a real reranker call.
// It is request-scoped so concurrent searches cannot borrow each other's state.
type RerankOutcome struct{ applied bool }

// WithRerankOutcome adds an initially-false rerank outcome to ctx.
func WithRerankOutcome(ctx context.Context) (context.Context, *RerankOutcome) {
	outcome := &RerankOutcome{}
	return context.WithValue(ctx, rerankOutcomeKey{}, outcome), outcome
}

// MarkRerankApplied records that the reranker client returned successfully.
func MarkRerankApplied(ctx context.Context) {
	if outcome, ok := ctx.Value(rerankOutcomeKey{}).(*RerankOutcome); ok && outcome != nil {
		outcome.applied = true
	}
}

// Applied reports whether this request made a successful reranker call.
func (o *RerankOutcome) Applied() bool {
	return o != nil && o.applied
}
