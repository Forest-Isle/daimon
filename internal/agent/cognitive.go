package agent

import "context"

// CognitiveKernel is a pluggable cognitive execution strategy invoked by
// HandleMessage in place of the legacy LinearLoop. The episode kernel
// implements it. The interface is declared here, at the use site, so the agent
// can delegate without importing the episode package (which imports agent).
type CognitiveKernel interface {
	Execute(ctx context.Context, req CognitiveRequest) (CognitiveOutcome, error)
}

// ToolInvokeFunc runs one tool call through the agent's full security pipeline
// (interceptor chain, approval, hooks) and event recording, returning the tool
// output and whether it failed. The kernel uses this instead of touching the
// tool registry directly, so episode tool calls get the same governance as the
// legacy path.
type ToolInvokeFunc func(ctx context.Context, iteration int, call ToolUseBlock) (output string, isError bool)

// CognitiveRequest carries everything the kernel needs for one turn. The
// runtime-owned context (persona, rules, retrieved memories, transcript) is
// pre-assembled by the agent so the kernel stays free of subsystem wiring.
type CognitiveRequest struct {
	SessionID  string
	Goal       string
	Trigger    string
	Persona    string
	Rules      string
	Memories   string
	Model      string
	Provider   string
	Transcript []CompletionMessage
	ToolDefs   []ToolDefinition
	Invoke     ToolInvokeFunc
}

// CognitiveOutcome is what the kernel returns. Reply is the user-facing text;
// Summary is the durable journal record. Status "failed" tells HandleMessage to
// fall back to the legacy path.
type CognitiveOutcome struct {
	Status  string
	Reply   string
	Summary string
}

// SetKernel wires a cognitive kernel and toggles whether HandleMessage routes
// through it. Passing nil or enabled=false keeps the legacy LinearLoop.
func (a *Agent) SetKernel(kernel CognitiveKernel, enabled bool) {
	a.kernel = kernel
	a.kernelEnabled = enabled
}
