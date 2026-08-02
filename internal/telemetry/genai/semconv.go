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
	OperationRetrieve    = "retrieve"
	OperationEmbedding   = "embedding"
	OperationRerank      = "rerank"
	OperationScorer      = "scorer"
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
// RAG / retrieval attribute keys (design spec §6.2)
// ---------------------------------------------------------------------------

const (
	// Corpus / index identity
	AttrCorpusGeneration = "rag.corpus_generation"
	AttrIndexAlias       = "rag.index_alias"
	AttrIndexPhysical    = "rag.index_physical"
	AttrMappingVersion   = "rag.mapping_version"

	// Query
	AttrQueryHash     = "rag.query_hash"
	AttrTopN          = "rag.top_n"
	AttrRetrievalMode = "rag.retrieval_mode"

	// Rerank / visual
	AttrRerankerApplied = "rag.reranker_applied"
	AttrVisualPath      = "rag.visual_path"

	// Privacy-safe content (hash/length only; never raw documents)
	AttrDocumentHash   = "rag.document_hash"
	AttrDocumentLength = "rag.document_length"
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
// RAG attribute constructors
// ---------------------------------------------------------------------------

func CorpusGenerationKV(generation string) attribute.KeyValue {
	return attribute.String(AttrCorpusGeneration, generation)
}

func IndexAliasKV(alias string) attribute.KeyValue {
	return attribute.String(AttrIndexAlias, alias)
}

func IndexPhysicalKV(index string) attribute.KeyValue {
	return attribute.String(AttrIndexPhysical, index)
}

func MappingVersionKV(version string) attribute.KeyValue {
	return attribute.String(AttrMappingVersion, version)
}

func QueryHashKV(hash string) attribute.KeyValue {
	return attribute.String(AttrQueryHash, hash)
}

func TopNKV(n int) attribute.KeyValue {
	return attribute.Int(AttrTopN, n)
}

func RetrievalModeKV(mode string) attribute.KeyValue {
	return attribute.String(AttrRetrievalMode, mode)
}

func RerankerAppliedKV(applied bool) attribute.KeyValue {
	return attribute.Bool(AttrRerankerApplied, applied)
}

func VisualPathKV(path string) attribute.KeyValue {
	return attribute.String(AttrVisualPath, path)
}

// Privacy-safe document attributes: hash + length only, never the raw
// document content (design spec §6.2: "默认不捕获完整 prompt/document；
// 只存 hash、长度和脱敏摘要").
func DocumentHashKV(hash string) attribute.KeyValue {
	return attribute.String(AttrDocumentHash, hash)
}

func DocumentLengthKV(n int) attribute.KeyValue {
	return attribute.Int(AttrDocumentLength, n)
}

// HashQuery returns a privacy-safe hash of a query (sha256 hex, truncated).
func HashQuery(query string) string {
	if query == "" {
		return ""
	}
	// Fast non-cryptographic stable hash for telemetry only; not used for
	// security.
	sum := fnv64(query)
	return strings.ToUpper(itoa64(sum))
}

func fnv64(s string) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func itoa64(n uint64) string {
	if n == 0 {
		return "0"
	}
	const hexDigits = "0123456789abcdef"
	digits := make([]byte, 0, 16)
	for n > 0 {
		digits = append([]byte{hexDigits[n&0xf]}, digits...)
		n >>= 4
	}
	return string(digits)
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
