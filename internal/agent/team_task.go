package agent

import (
	"fmt"
	"sync"
)

// TaskStatus represents the state of a team task.
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskCompleted  TaskStatus = "completed"
	TaskBlocked    TaskStatus = "blocked"
)

// TeamTask is a unit of work in the shared task list.
type TeamTask struct {
	ID          string     `json:"id"`
	Subject     string     `json:"subject"`
	Description string     `json:"description"`
	Owner       string     `json:"owner,omitempty"`
	Status      TaskStatus `json:"status"`
	BlockedBy   []string   `json:"blocked_by,omitempty"`
}

// TeamTaskList is a shared, thread-safe task list for team coordination.
type TeamTaskList struct {
	mu     sync.RWMutex
	tasks  []*TeamTask
	nextID int
}

// NewTeamTaskList creates an empty shared task list.
func NewTeamTaskList() *TeamTaskList {
	return &TeamTaskList{nextID: 1}
}

// Create adds a new task with pending status.
func (l *TeamTaskList) Create(subject, description string) *TeamTask {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := &TeamTask{
		ID:          fmt.Sprintf("%d", l.nextID),
		Subject:     subject,
		Description: description,
		Status:      TaskPending,
	}
	l.nextID++
	l.tasks = append(l.tasks, t)
	return t
}

// Get returns a copy of the task with the given ID, or nil.
func (l *TeamTaskList) Get(id string) *TeamTask {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, t := range l.tasks {
		if t.ID == id {
			cp := *t
			return &cp
		}
	}
	return nil
}

// UpdateStatus changes a task's status. Returns true if found.
func (l *TeamTaskList) UpdateStatus(id string, status TaskStatus) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, t := range l.tasks {
		if t.ID == id {
			t.Status = status
			return true
		}
	}
	return false
}

// Assign sets the owner of a task. Returns true if found.
func (l *TeamTaskList) Assign(id, owner string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, t := range l.tasks {
		if t.ID == id {
			t.Owner = owner
			return true
		}
	}
	return false
}

// Available returns unassigned, unblocked, pending tasks.
func (l *TeamTaskList) Available() []*TeamTask {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var avail []*TeamTask
	for _, t := range l.tasks {
		if t.Status == TaskPending && t.Owner == "" && len(t.BlockedBy) == 0 {
			cp := *t
			avail = append(avail, &cp)
		}
	}
	return avail
}

// All returns copies of all tasks.
func (l *TeamTaskList) All() []*TeamTask {
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]*TeamTask, len(l.tasks))
	for i, t := range l.tasks {
		cp := *t
		result[i] = &cp
	}
	return result
}
