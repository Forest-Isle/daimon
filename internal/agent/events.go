package agent

import "encoding/json"

// Event is the base interface for all agent lifecycle events.
type Event interface {
	EventType() string
}

// ──────────────────────── Session Events ────────────────────────

// SessionStarted is emitted when a new agent session begins.
type SessionStarted struct {
	SessionID string
	Channel   string
}

func (SessionStarted) EventType() string { return "session.started" }

// SessionEnded is emitted when an agent session completes.
type SessionEnded struct {
	SessionID  string
	Succeeded  bool
	DurationMs int64
}

func (SessionEnded) EventType() string { return "session.ended" }

// ──────────────────────── Tool Events ────────────────────────

// ToolExecuted is emitted after each tool execution completes.
type ToolExecuted struct {
	SessionID  string
	ToolName   string
	Succeeded  bool
	DurationMs int64
	Error      string
}

func (ToolExecuted) EventType() string { return "tool.executed" }

// PermissionDecision is emitted whenever runtime policy evaluates a tool call.
type PermissionDecision struct {
	SessionID    string
	ToolName     string
	Action       string
	Reason       string
	MatchedRule  string
	ChannelClass string
}

func (PermissionDecision) EventType() string { return "permission.decision" }

// ──────────────────────── Model Events ────────────────────────

// ModelCallStarted is emitted immediately before a provider request.
type ModelCallStarted struct {
	SessionID    string
	Iteration    int
	Model        string
	Provider     string
	MessageCount int
	ToolCount    int
	SystemChars  int
	Streaming    bool
}

func (ModelCallStarted) EventType() string { return "model.call.started" }

// ModelCallEnded is emitted after a provider request succeeds or fails.
type ModelCallEnded struct {
	SessionID  string
	Iteration  int
	Model      string
	Provider   string
	Streaming  bool
	Succeeded  bool
	DurationMs int64
	StopReason string
	Error      string
}

func (ModelCallEnded) EventType() string { return "model.call.ended" }

// ──────────────────────── Replay Events ────────────────────────

// ProviderExchange is emitted once per provider call with replay-grade request
// and response payloads.
type ProviderExchange struct {
	SessionID     string
	Iteration     int
	Model         string
	Provider      string
	SystemPrompt  string
	MessagesJSON  json.RawMessage
	ResponseText  string
	ToolCallsJSON json.RawMessage
	StopReason    string
	DurationMs    int64
}

func (ProviderExchange) EventType() string { return "replay.provider_exchange" }

// ToolRoundTrip is emitted after each tool execution with full arguments and
// result payloads.
type ToolRoundTrip struct {
	SessionID  string
	Iteration  int
	ToolName   string
	ArgsJSON   json.RawMessage
	ResultJSON json.RawMessage
	Succeeded  bool
	DurationMs int64
}

func (ToolRoundTrip) EventType() string { return "replay.tool_round_trip" }

// TurnClosed is emitted when the agent loop returns the final user-facing reply.
type TurnClosed struct {
	SessionID  string
	FinalReply string
}

func (TurnClosed) EventType() string { return "replay.turn_closed" }

// ──────────────────────── Context Events ────────────────────────

// ContextCompressed is emitted after context compression runs.
type ContextCompressed struct {
	SessionID string
	Reason    string
	LayersRun int
	BeforePct float64
	AfterPct  float64
}

func (ContextCompressed) EventType() string { return "context.compressed" }

// ──────────────────────── Sub-Agent Events ────────────────────────

// SubAgentSpawned is emitted when a sub-agent is created.
type SubAgentSpawned struct {
	SessionID       string
	ParentSessionID string
	AgentName       string
	Task            string
}

func (SubAgentSpawned) EventType() string { return "subagent.spawned" }

// SubAgentCompleted is emitted when a sub-agent finishes.
type SubAgentCompleted struct {
	SessionID  string
	AgentName  string
	Succeeded  bool
	DurationMs int64
}

func (SubAgentCompleted) EventType() string { return "subagent.completed" }

// ──────────────────────── Metrics Events ────────────────────────

// MetricsTick is emitted on each agent iteration with runtime metrics.
type MetricsTick struct {
	SessionID    string
	Iteration    int
	MaxIter      int
	Utilization  float64
	InputTokens  int64
	OutputTokens int64
	CacheCreate  int64
	CacheRead    int64
	Model        string
	Provider     string
}

func (MetricsTick) EventType() string { return "metrics.tick" }

// PromptFrameRendered is emitted whenever a prompt frame is rendered into the
// system prompt sent to the provider or used for context budgeting.
type PromptFrameRendered struct {
	SessionID       string
	Iteration       int // -1 means pre-loop render
	LayerCount      int
	LayerKeys       []string
	ScopeCounts     map[PromptLayerScope]int
	CharacterCount  int
	EstimatedTokens int
}

func (PromptFrameRendered) EventType() string { return "prompt_frame.rendered" }

// ──────────────────────── Config Events ────────────────────────

// ConfigChanged is emitted when the config file is hot-reloaded.
type ConfigChanged struct {
	Path string
}

func (ConfigChanged) EventType() string { return "config.changed" }

// ──────────────────────── Workflow Events ────────────────────────

type WorkflowStepEvent struct {
	WorkflowName string
	WorkflowHash string
	StageID      string
	StepID       string
	StepType     string
	Phase        string
	Status       string
	Cached       bool
	DurationMs   int64
	Error        string
}

func (WorkflowStepEvent) EventType() string { return "workflow.step" }

// ──────────────────────── Task Events ────────────────────────

type TaskTransitioned struct {
	TaskID    string
	Kind      string
	FromState string
	ToState   string
	Reason    string
}

func (TaskTransitioned) EventType() string { return "task.transitioned" }
