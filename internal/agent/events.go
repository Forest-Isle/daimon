package agent

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

// ──────────────────────── Phase Events ────────────────────────

// PhaseChanged is emitted when the agent enters or exits a phase.
type PhaseChanged struct {
	SessionID string
	Phase     string
	IsStart   bool // true = entering phase, false = exiting
}

func (PhaseChanged) EventType() string { return "phase.changed" }

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

// ──────────────────────── Plan Events ────────────────────────

// PlanGenerated is emitted after the PLAN phase produces a task plan.
type PlanGenerated struct {
	SessionID      string
	TaskCount      int
	Complexity     string
	HasDirectReply bool
}

func (PlanGenerated) EventType() string { return "plan.generated" }

// ReplanStarted is emitted when a replan is triggered.
type ReplanStarted struct {
	SessionID string
	Attempt   int
	Reason    string
}

func (ReplanStarted) EventType() string { return "replan.started" }

// ──────────────────────── Observation Events ────────────────────────

// ObservationCompleted is emitted after the OBSERVE phase.
type ObservationCompleted struct {
	SessionID       string
	Passed          int
	Failed          int
	Total           int
	OverallProgress float64
}

func (ObservationCompleted) EventType() string { return "observation.completed" }

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

// ──────────────────────── Config Events ────────────────────────

// ConfigChanged is emitted when the config file is hot-reloaded.
type ConfigChanged struct {
	Path string
}

func (ConfigChanged) EventType() string { return "config.changed" }
