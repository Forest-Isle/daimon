package observability

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// OpenInference span kind constants — used as custom attributes for
// compatibility with OpenInference-compatible observability backends
// (Arize Phoenix, Grafana, LangSmith, traceAI, etc.).
const (
	OpenInferenceSpanKindLLM       = "LLM"
	OpenInferenceSpanKindTool      = "TOOL"
	OpenInferenceSpanKindChain     = "CHAIN"
	OpenInferenceSpanKindRetriever = "RETRIEVER"
	OpenInferenceSpanKindAgent     = "AGENT"
	OpenInferenceSpanKindReranker  = "RERANKER"
	OpenInferenceSpanKindGuardrail = "GUARDRAIL"
	OpenInferenceSpanKindEmbedding = "EMBEDDING"
)

// openInferenceSpanKindKey is the canonical OpenInference span kind attribute name.
const openInferenceSpanKindKey = "openinference.span.kind"

// ---------------------------------------------------------------------------
// OTel-GenAI semantic convention attributes
// See: https://github.com/open-telemetry/semantic-conventions/blob/main/docs/gen-ai/
// ---------------------------------------------------------------------------

const (
	// GenAISystem identifies the generative AI provider.
	GenAISystemKey = "gen_ai.system"
	// GenAIRequestModel is the model name used for the request.
	GenAIRequestModelKey = "gen_ai.request.model"
	// GenAIRequestMaxTokens is the max tokens parameter.
	GenAIRequestMaxTokensKey = "gen_ai.request.max_tokens"
	// GenAIUsageInputTokens is the number of input (prompt) tokens.
	GenAIUsageInputTokensKey = "gen_ai.usage.input_tokens"
	// GenAIUsageOutputTokens is the number of output (completion) tokens.
	GenAIUsageOutputTokensKey = "gen_ai.usage.output_tokens"
	// GenAIResponseID is the provider-assigned response identifier.
	GenAIResponseIDKey = "gen_ai.response.id"
	// GenAIOperationName is one of "chat", "embeddings", "text_completion".
	GenAIOperationNameKey = "gen_ai.operation.name"
)

// LLMSpanAttributes returns OTel attributes for an LLM call span, covering
// both OpenInference and OTel-GenAI semantic conventions.
func LLMSpanAttributes(provider, model string, inputTokens, outputTokens int, responseID string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openInferenceSpanKindKey, OpenInferenceSpanKindLLM),
		attribute.String(GenAISystemKey, provider),
		attribute.String(GenAIRequestModelKey, model),
		attribute.String(GenAIOperationNameKey, "chat"),
	}
	if inputTokens > 0 {
		attrs = append(attrs, attribute.Int(GenAIUsageInputTokensKey, inputTokens))
	}
	if outputTokens > 0 {
		attrs = append(attrs, attribute.Int(GenAIUsageOutputTokensKey, outputTokens))
	}
	if responseID != "" {
		attrs = append(attrs, attribute.String(GenAIResponseIDKey, responseID))
	}
	return attrs
}

// ToolSpanAttributes returns OTel attributes for a tool execution span,
// covering both OpenInference and semantic conventions.
func ToolSpanAttributes(toolName, sessionID string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openInferenceSpanKindKey, OpenInferenceSpanKindTool),
		attribute.String("tool.name", toolName),
	}
	if sessionID != "" {
		attrs = append(attrs, attribute.String("session.id", sessionID))
	}
	return attrs
}

// AgentSpanAttributes returns OTel attributes for an agent-level span
// (cognitive loop, sub-agent spawn, etc.).
func AgentSpanAttributes(agentName string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openInferenceSpanKindKey, OpenInferenceSpanKindAgent),
	}
	if agentName != "" {
		attrs = append(attrs, attribute.String("agent.name", agentName))
	}
	return attrs
}

// RetrieverSpanAttributes returns OTel attributes for a retrieval span
// (memory search, knowledge base query, hybrid retrieval).
func RetrieverSpanAttributes(source, query string, resultCount int) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openInferenceSpanKindKey, OpenInferenceSpanKindRetriever),
		attribute.String("retrieval.source", source),
	}
	if query != "" {
		attrs = append(attrs, attribute.String("retrieval.query", query))
	}
	if resultCount > 0 {
		attrs = append(attrs, attribute.Int("retrieval.result_count", resultCount))
	}
	return attrs
}

// ChainSpanAttributes returns OTel attributes for a chain/sequence span
// (context compression, interceptor chains).
func ChainSpanAttributes(chainName string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openInferenceSpanKindKey, OpenInferenceSpanKindChain),
	}
	if chainName != "" {
		attrs = append(attrs, attribute.String("chain.name", chainName))
	}
	return attrs
}

// EmbeddingSpanAttributes returns OTel attributes for an embedding generation span.
func EmbeddingSpanAttributes(model string, batchSize int) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(openInferenceSpanKindKey, OpenInferenceSpanKindEmbedding),
		attribute.String(GenAISystemKey, "openai"),
		attribute.String(GenAIOperationNameKey, "embeddings"),
	}
	if model != "" {
		attrs = append(attrs, attribute.String(GenAIRequestModelKey, model))
	}
	if batchSize > 0 {
		attrs = append(attrs, attribute.Int("embedding.batch_size", batchSize))
	}
	return attrs
}

// WithSpanKind returns a SpanStartOption that sets the OTel SpanKind.
func WithSpanKind(kind trace.SpanKind) trace.SpanStartOption {
	return trace.WithSpanKind(kind)
}
