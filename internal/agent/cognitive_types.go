package agent

// SubTaskStatus tracks the lifecycle of a single subtask.
type SubTaskStatus string

const (
	SubTaskPending SubTaskStatus = "pending"
	SubTaskRunning SubTaskStatus = "running"
	SubTaskDone    SubTaskStatus = "done"
	SubTaskFailed  SubTaskStatus = "failed"
	SubTaskSkipped SubTaskStatus = "skipped"
)

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
	Metadata   map[string]any // structured metadata from tool.Result (status_code, result_count, etc.)
}
