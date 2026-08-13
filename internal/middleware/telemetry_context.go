package middleware

import (
	"code-agent/internal/telemetry/genai"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
)

// TraceContextMiddleware restores the remote W3C parent for internal HTTP
// calls. Only the two evaluation join keys are admitted from baggage; copying
// arbitrary caller-controlled baggage into telemetry would leak metadata and
// make trace evidence ambiguous.
func TraceContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		carrier := propagation.HeaderCarrier(c.Request.Header)
		ctx := propagation.TraceContext{}.Extract(c.Request.Context(), carrier)
		extracted := propagation.Baggage{}.Extract(ctx, carrier)

		members := make([]baggage.Member, 0, 2)
		for _, key := range []string{genai.AttrEvalRunID, genai.AttrEvalInstanceID} {
			member := baggage.FromContext(extracted).Member(key)
			if member.Value() != "" {
				members = append(members, member)
			}
		}
		if bag, err := baggage.New(members...); err == nil {
			ctx = baggage.ContextWithBaggage(ctx, bag)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
