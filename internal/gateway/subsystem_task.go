package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/Forest-Isle/IronClaw/internal/agent"
	"github.com/Forest-Isle/IronClaw/internal/taskledger"
)

// TaskSubsystem manages the task ledger, sub-agent manager, and stale task detector.
type TaskSubsystem struct {
	taskLedger    *taskledger.SQLiteTaskLedger
	subAgentMgr   *agent.SubAgentManager
	staleDetector *taskledger.StaleDetector
}

func (ts *TaskSubsystem) Name() string { return "task" }

// Start starts the stale task detector. No-op if the ledger is nil.
func (ts *TaskSubsystem) Start(ctx context.Context) error {
	if ts.taskLedger == nil {
		return nil
	}
	ts.staleDetector = taskledger.NewStaleDetector(
		ts.taskLedger, 2*time.Minute, 30*time.Second,
		func(t taskledger.Task) {
			slog.Info("stale-detector: task marked stale", "id", t.ID, "title", t.Title)
		},
	)
	ts.staleDetector.Start()
	slog.Info("stale task detector started")
	return nil
}

// Stop stops the stale detector.
func (ts *TaskSubsystem) Stop(_ context.Context) error {
	if ts.staleDetector != nil {
		ts.staleDetector.Stop()
	}
	return nil
}

// TaskLedger returns the task ledger, or nil.
func (ts *TaskSubsystem) TaskLedger() *taskledger.SQLiteTaskLedger { return ts.taskLedger }

// SubAgentManager returns the sub-agent manager, or nil.
func (ts *TaskSubsystem) SubAgentManager() *agent.SubAgentManager { return ts.subAgentMgr }

