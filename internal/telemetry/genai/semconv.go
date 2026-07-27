// Package genai provides OpenTelemetry GenAI semantic convention constants and a
// lightweight telemetry helper for instrumenting agent loops with gen_ai.* spans.
//
// Attributes follow a dual-emit pattern: the legacy key gen_ai.system and the
// current key gen_ai.provider.name are both set on every span so older and
// newer OTel exporters work. Set the environment variable
// OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental to emit only the
// newest keys.
package genai

import (
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

// ---------------------------------------------------------------------------
// Operation names — used as gen_ai.operation.name values
// ---------------------------------------------------------------------------

const (
	OperationInvokeAgent = "invoke_agent"
	OperationInference   = "inference"
	OperationExecuteTool = "execute_tool"
	OperationPlan        = "plan"
)

// ---------------------------------------------------------------------------
// System / provider
// ---------------------------------------------------------------------------

const SystemGenAI = "gen_ai"

// ---------------------------------------------------------------------------
// Attribute keys — current gen_ai.* namespace
// ---------------------------------------------------------------------------

const (
	// Provider / operation
	AttrProviderName  = "gen_ai.provider.name"
	AttrOperationName = "gen_ai.operation.name"

	// Model
	AttrRequestModel       = "gen_ai.request.model"
	AttrResponseModel      = "gen_ai.response.model"
	AttrRequestMaxTokens   = "gen_ai.request.max_tokens"
	AttrRequestTemperature = "gen_ai.request.temperature"

	// Usage
	AttrUsageInputTokens       = "gen_ai.usage.input_tokens"
	AttrUsageOutputTokens      = "gen_ai.usage.output_tokens"
	AttrUsageCachedInputTokens = "gen_ai.usage.cache_read.input_tokens"

	// Tool
	AttrToolName        = "gen_ai.tool.name"
	AttrToolCallID      = "gen_ai.tool.call.id"
	AttrToolCallArgs    = "gen_ai.tool.call.arguments"
	AttrToolCallResult  = "gen_ai.tool.call.result"
	AttrToolType        = "gen_ai.tool.type"

	// Agent / response
	AttrAgentName     = "gen_ai.agent.name"
	AttrResponseID    = "gen_ai.response.id"
	AttrFinishReasons = "gen_ai.response.finish_reasons"
)

// ---------------------------------------------------------------------------
// Legacy keys — emitted alongside the current keys unless the caller has
// opted into gen_ai_latest_experimental.
// ---------------------------------------------------------------------------

const attrSystemLegacy = "gen_ai.system" // deprecated in favour of gen_ai.provider.name

// optInExperimental reports whether OTEL_SEMCONV_STABILITY_OPT_IN includes
// gen_ai_latest_experimental.
func optInExperimental() bool {
	for _, token := range strings.Split(os.Getenv("OTEL_SEMCONV_STABILITY_OPT_IN"), ",") {
		if strings.TrimSpace(token) == "gen_ai_latest_experimental" {
			return true
		}
	}
	return false
}

// GenAIAttributes returns the base attribute set for every gen_ai span.
// It always includes gen_ai.operation.name and gen_ai.provider.name.
// When opt-in is NOT set it also dual-emits gen_ai.system (legacy).
func GenAIAttributes(operation, provider string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(AttrOperationName, operation),
		attribute.String(AttrProviderName, provider),
	}
	if optInExperimental() {
		return attrs
	}
	return append(attrs, attribute.String(attrSystemLegacy, provider))
}

// ---------------------------------------------------------------------------
// Convenience constructors for common span attributes
// ---------------------------------------------------------------------------

func AgentNameKV(name string) attribute.KeyValue {
	return attribute.String(AttrAgentName, name)
}

func ToolNameKV(name string) attribute.KeyValue {
	return attribute.String(AttrToolName, name)
}

func ToolCallIDKV(id string) attribute.KeyValue {
	return attribute.String(AttrToolCallID, id)
}

func ToolCallArgsKV(args string) attribute.KeyValue {
	return truncate(AttrToolCallArgs, args, 1024)
}

func ToolCallResultKV(result string) attribute.KeyValue {
	return truncate(AttrToolCallResult, result, 1024)
}

func ToolTypeKV(typ string) attribute.KeyValue {
	return attribute.String(AttrToolType, typ)
}

func RequestModelKV(model string) attribute.KeyValue {
	return attribute.String(AttrRequestModel, model)
}

func UsageInputTokensKV(n int64) attribute.KeyValue {
	return attribute.Int64(AttrUsageInputTokens, n)
}

func UsageOutputTokensKV(n int64) attribute.KeyValue {
	return attribute.Int64(AttrUsageOutputTokens, n)
}

func UsageCachedInputTokensKV(n int64) attribute.KeyValue {
	return attribute.Int64(AttrUsageCachedInputTokens, n)
}

func FinishReasonsKV(reasons string) attribute.KeyValue {
	return attribute.String(AttrFinishReasons, reasons)
}

// ---------------------------------------------------------------------------
// internal
// ---------------------------------------------------------------------------

func truncate(key, value string, max int) attribute.KeyValue {
	if len(value) <= max {
		return attribute.String(key, value)
	}
	return attribute.String(key, value[:max-3]+"...")
}
