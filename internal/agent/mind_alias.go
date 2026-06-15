package agent

// Bridge: the LLM provider / cognition contract moved to internal/mind during the
// P3-I mind-split (re-founding thesis #2 — agent is the immortal biography, the model
// is a swappable cognition engine). These aliases re-export every moved symbol so
// existing agent.X references keep compiling while callers migrate to mind.X. The file
// is deleted in the final stage once no external caller references the contract via agent.
//
// This is a pure re-export: no behavior, no new symbols.

import "github.com/Forest-Isle/daimon/internal/mind"

// Provider contract + data types.
type (
	Provider             = mind.Provider
	CompletionRequest    = mind.CompletionRequest
	CompletionResponse   = mind.CompletionResponse
	CompletionMessage    = mind.CompletionMessage
	ToolDefinition       = mind.ToolDefinition
	ToolUseBlock         = mind.ToolUseBlock
	ResponseFormat       = mind.ResponseFormat
	JSONSchema           = mind.JSONSchema
	StopReason           = mind.StopReason
	Usage                = mind.Usage
	StreamDelta          = mind.StreamDelta
	StreamIterator       = mind.StreamIterator
	ClaudeProvider       = mind.ClaudeProvider
	OpenAIProvider       = mind.OpenAIProvider
	RetryProvider        = mind.RetryProvider
	CircuitState         = mind.CircuitState
	CircuitBreaker       = mind.CircuitBreaker
	APIPromptCacheStats  = mind.APIPromptCacheStats
	CacheMetrics         = mind.CacheMetrics
	CacheMetricsSnapshot = mind.CacheMetricsSnapshot
)

// Typed constants.
const (
	StopEndTurn  = mind.StopEndTurn
	StopToolUse  = mind.StopToolUse
	StopMaxToken = mind.StopMaxToken
	StopAbnormal = mind.StopAbnormal

	CircuitClosed   = mind.CircuitClosed
	CircuitOpen     = mind.CircuitOpen
	CircuitHalfOpen = mind.CircuitHalfOpen

	// dynamicContextMarker: the cache-split delimiter now lives in mind as the shared
	// prompt-cache protocol token; re-exported lowercase so agent prompt-framing
	// (prompt_frame.go, context_manager.go) keeps referencing it unqualified.
	dynamicContextMarker = mind.DynamicContextMarker
)

// Constructors, helpers, sentinel errors (re-exported as values).
var (
	NewProviderFromConfig = mind.NewProviderFromConfig
	NewClaudeProvider     = mind.NewClaudeProvider
	NewOpenAIProvider     = mind.NewOpenAIProvider
	NewRetryProvider      = mind.NewRetryProvider
	NewCircuitBreaker     = mind.NewCircuitBreaker
	NewCacheMetrics       = mind.NewCacheMetrics
	ModelContextWindow    = mind.ModelContextWindow

	ErrCircuitOpen = mind.ErrCircuitOpen
)
