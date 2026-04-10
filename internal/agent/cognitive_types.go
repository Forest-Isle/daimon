package agent

import (
	"context"
	"sync"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/rl"
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
	Agents           string   // injected agent prompt section (may be empty)
	KnowledgeContext []string // relevant knowledge base snippets
	GraphContext     []string // relevant knowledge graph relations
	Personality      string   // from Soul.md — persona/style for final_answer tone
	PersistentRules  string   // from Memory.md — rules all phases must follow
	Preferences      string   // learned user preferences from evolution PreferenceLearner
	StrategyHints    string   // tuned cognitive strategy hints from evolution StrategyOptimizer
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

// DimensionScore represents a single evaluation dimension in reflection scoring.
type DimensionScore struct {
	Score       int    `json:"score"`                // 0–25
	Explanation string `json:"explanation,omitempty"` // justification for the score
}

// ReflectReasoning captures the multi-dimensional evaluation breakdown.
// Each dimension is scored 0–25; overall_confidence = sum / 100.
type ReflectReasoning struct {
	Completeness   DimensionScore `json:"completeness"`
	Accuracy       DimensionScore `json:"accuracy"`
	Efficiency     DimensionScore `json:"efficiency"`
	Relevance      DimensionScore `json:"relevance"`
	KeyImprovement string         `json:"key_improvement,omitempty"`
}

// DerivedConfidence computes confidence from dimension scores (sum / 100),
// clamped to [0.0, 1.0].
func (r *ReflectReasoning) DerivedConfidence() float64 {
	sum := r.Completeness.Score + r.Accuracy.Score + r.Efficiency.Score + r.Relevance.Score
	c := float64(sum) / 100.0
	if c < 0 {
		return 0
	}
	if c > 1 {
		return 1
	}
	return c
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
	Reasoning           *ReflectReasoning // multi-dimensional evaluation breakdown (nil for legacy responses)
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
	OverallConfidence   float64           `json:"overall_confidence"`
	Succeeded           bool              `json:"succeeded"`
	LessonsLearned      []string          `json:"lessons_learned"`
	SuggestedAdjustment string            `json:"suggested_adjustment"`
	FinalAnswer         string            `json:"final_answer"`
	NeedsReplan         bool              `json:"needs_replan"`
	ReplanReason        string            `json:"replan_reason"`
	Reasoning           *ReflectReasoning `json:"reasoning,omitempty"`
}

// RLPolicy is the interface for RL policy integration.
type RLPolicy interface {
	IsEnabled() bool
	SelectTool(ctx context.Context, state *rl.RLState, toolNames []string) *rl.ToolSelectionAction
	UpdateToolSelection(ctx context.Context, state *rl.RLState, toolName string, reward float64) error
	SelectPlanStrategy(state *rl.RLState) *rl.PlanStrategyAction
	SelectReplanAction(state *rl.RLState) rl.ReplanActionType
}

// RLTrainer is the interface for RL training coordination.
type RLTrainer interface {
	AddExperience(exp rl.Experience)
	RecordEpisode(ctx context.Context, params rl.EpisodeParams) error
}

// FeedbackCollector collects user satisfaction feedback from the channel.
// Returns feedback in [-1, 1]: negative, neutral, or positive.
// Channels that do not implement FeedbackSender yield 0 (neutral).
// Errors (e.g., timeout, network issue) also yield 0 (neutral).
type FeedbackCollector func(ctx context.Context, ch channel.Channel, target channel.MessageTarget) float64

// EpisodeCollector accumulates RL experiences during one cognitive loop pass.
type EpisodeCollector struct {
	State       *rl.RLState
	StartTime   time.Time
	mu          sync.Mutex
	experiences []rl.Experience
}

// Add appends an experience to the collector (thread-safe).
func (c *EpisodeCollector) Add(exp rl.Experience) {
	c.mu.Lock()
	c.experiences = append(c.experiences, exp)
	c.mu.Unlock()
}

// GetExperiences returns a copy of all collected experiences.
func (c *EpisodeCollector) GetExperiences() []rl.Experience {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]rl.Experience, len(c.experiences))
	copy(out, c.experiences)
	return out
}
