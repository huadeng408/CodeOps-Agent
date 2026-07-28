// Package service contains business logic.
package service

import "context"

type searchContextKey string

const disableRerankContextKey searchContextKey = "disable_rerank"

// WithRerankDisabled returns a context with rerank disabled.
func WithRerankDisabled(ctx context.Context) context.Context {
	return context.WithValue(ctx, disableRerankContextKey, true)
}

// rerankDisabled handles rerank disabled.
func rerankDisabled(ctx context.Context) bool {
	disabled, ok := ctx.Value(disableRerankContextKey).(bool)
	return ok && disabled
}
