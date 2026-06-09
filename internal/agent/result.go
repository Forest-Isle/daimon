package agent

import (
	"time"
)

// AgentResult captures the outcome of a sub-agent execution.
type AgentResult struct {
	AgentName  string
	TaskID     string
	Output     string
	Error      error
	Duration   time.Duration
	TokenUsage TokenUsage
}

// TokenUsage tracks token consumption for a single agent invocation.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}
