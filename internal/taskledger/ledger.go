package taskledger

import (
	"context"
	"time"
)

type TaskState string

const (
	TaskStatePending   TaskState = "pending"
	TaskStateRunning   TaskState = "running"
	TaskStateCompleted TaskState = "completed"
	TaskStateFailed    TaskState = "failed"
	TaskStateCancelled TaskState = "cancelled"
)

type TaskKind string

const (
	TaskKindUserRequest      TaskKind = "user_request"
	TaskKindCognitiveSubtask TaskKind = "cognitive_subtask"
	TaskKindSubAgent         TaskKind = "sub_agent"
	TaskKindScheduled        TaskKind = "scheduled"
	TaskKindTeamTask         TaskKind = "team_task"
)

type Task struct {
	ID          string
	ParentID    string // empty for root tasks
	Kind        TaskKind
	State       TaskState
	Title       string
	Description string
	Assignee    string // agent ID or empty
	DependsOn   []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
	Heartbeat   *time.Time
	Result      string // completion summary or error message
	Metadata    map[string]string
}

type TaskFilter struct {
	State    *TaskState
	Kind     *TaskKind
	ParentID *string
}

type TaskLedger interface {
	Register(ctx context.Context, task Task) error
	Get(ctx context.Context, id string) (*Task, error)
	Update(ctx context.Context, task Task) error
	List(ctx context.Context, filter TaskFilter) ([]Task, error)
	Cancel(ctx context.Context, id string, reason string) error
	ClaimNext(ctx context.Context, kind TaskKind, assignee string) (*Task, error)
	Heartbeat(ctx context.Context, id string) error
	GetTree(ctx context.Context, rootID string) ([]Task, error)
	DetectStale(ctx context.Context, timeout time.Duration) ([]Task, error)
}
