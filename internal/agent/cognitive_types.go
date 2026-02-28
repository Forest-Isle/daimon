package agent

import (
	"github.com/punkopunko/ironclaw/internal/memory"
)

// TaskComplexity classifies how complex a user request is.
type TaskComplexity string

const (
	ComplexitySimple   TaskComplexity = "simple"
	ComplexityModerate TaskComplexity = "moderate"
	ComplexityComplex  TaskComplexity = "complex"
)

// SubTaskStatus tracks the lifecycle of a single subtask.
type SubTaskStatus string

const (
	SubTaskPending  SubTaskStatus = "pending"
	SubTaskRunning  SubTaskStatus = "running"
	SubTaskDone     SubTaskStatus = "done"
	SubTaskFailed   SubTaskStatus = "failed"
	SubTaskSkipped  SubTaskStatus = "skipped"
)

// ReplanDecision is the user's choice when confidence is low.
type ReplanDecision string

const (
	ReplanContinue ReplanDecision = "continue"
	ReplanAdjust   ReplanDecision = "adjust"
	ReplanAbort    ReplanDecision = "abort"
)

// Goal captures the parsed intent from the user message.
type Goal struct {
	Raw        string
	Intent     string
	Complexity TaskComplexity
}

// CognitiveState is the output of the PERCEIVE phase.
type CognitiveState struct {
	SessionID        string
	UserID           string // identifies the user across sessions
	UserMessage      string
	Goal             Goal
	RelevantMemories []memory.SearchResult
	RecentHistory    []CompletionMessage
	Skills           string   // injected skill prompt section (may be empty)
	KnowledgeContext []string // relevant knowledge base snippets
	GraphContext     []string // relevant knowledge graph relations
	Personality      string   // from Soul.md — persona/style for final_answer tone
	PersistentRules  string   // from Memory.md — rules all phases must follow
}

// SubTask is a single unit of work within a TaskPlan.
type SubTask struct {
	ID          string
	Description string
	ToolName    string   // empty = LLM generates text directly
	ToolInput   string   // raw JSON input for the tool
	DependsOn   []string // IDs of subtasks that must complete first
	Confidence  float64
	Status      SubTaskStatus
}

// TaskPlan is the output of the PLAN phase.
type TaskPlan struct {
	Summary           string
	SubTasks          []*SubTask
	OverallConfidence float64
	DirectReply       string // non-empty: skip ACT/OBSERVE and reply directly
	ReplanCount       int
}

// Observation records the outcome of a single subtask execution.
type Observation struct {
	SubTaskID  string
	ToolName   string
	Input      string
	Output     string
	Error      string
	DurationMs int64
	Denied     bool
}

// ObservationResult is the aggregate of all observations (output of OBSERVE phase).
type ObservationResult struct {
	Observations    []Observation
	SuccessCount    int
	FailureCount    int
	DeniedCount     int
	OverallProgress float64 // 0.0–1.0
	ErrorPatterns   []string
}

// Reflection is the output of the REFLECT phase.
type Reflection struct {
	OverallConfidence   float64
	Succeeded           bool
	LessonsLearned      []string
	SuggestedAdjustment string
	FinalAnswer         string
	NeedsReplan         bool
	ReplanReason        string
}

// planJSON is the raw JSON structure returned by the LLM during PLAN.
type planJSON struct {
	Summary           string        `json:"summary"`
	SubTasks          []subTaskJSON `json:"sub_tasks"`
	OverallConfidence float64       `json:"overall_confidence"`
	DirectReply       string        `json:"direct_reply"`
}

type subTaskJSON struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	ToolName    string   `json:"tool_name"`
	ToolInput   string   `json:"tool_input"`
	DependsOn   []string `json:"depends_on"`
	Confidence  float64  `json:"confidence"`
}

// reflectJSON is the raw JSON structure returned by the LLM during REFLECT.
type reflectJSON struct {
	OverallConfidence   float64  `json:"overall_confidence"`
	Succeeded           bool     `json:"succeeded"`
	LessonsLearned      []string `json:"lessons_learned"`
	SuggestedAdjustment string   `json:"suggested_adjustment"`
	FinalAnswer         string   `json:"final_answer"`
	NeedsReplan         bool     `json:"needs_replan"`
	ReplanReason        string   `json:"replan_reason"`
}
