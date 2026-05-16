package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/Forest-Isle/IronClaw/internal/channel"
	"github.com/Forest-Isle/IronClaw/internal/session"
)

// StreamingExecutor wraps the existing Executor with streaming progress.
type StreamingExecutor struct {
	inner *Executor
}

func NewStreamingExecutor(inner *Executor) *StreamingExecutor {
	return &StreamingExecutor{inner: inner}
}

// Stream executes subtasks as they arrive from the planner.
func (se *StreamingExecutor) Stream(
	ctx context.Context,
	ch channel.Channel,
	sess *session.Session,
	target channel.MessageTarget,
	subtaskCh <-chan *SubTask,
	obsCh chan<- *Observation,
	streamOut chan<- string,
) error {
	maxParallel := se.inner.cfg.MaxParallelTools
	if maxParallel <= 0 {
		maxParallel = 3
	}

	pending := make(map[string]*SubTask)
	completed := make(map[string]bool)
	allTasks := make([]*SubTask, 0, 8)
	sem := make(chan struct{}, maxParallel)

	var (
		mu            sync.Mutex
		wg            sync.WaitGroup
		totalSeen     int
		completedCnt  int
		scheduleReady func()
	)

	startTask := func(st *SubTask) {
		taskSnapshot := append([]*SubTask(nil), allTasks...)
		sem <- struct{}{}
		wg.Add(1)
		go func(subtask *SubTask) {
			defer wg.Done()
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			mu.Lock()
			subtask.Status = SubTaskRunning
			mu.Unlock()

			obs := se.inner.executeSubTask(ctx, ch, sess, target, subtask, taskSnapshot, nil, &TaskPlan{SubTasks: taskSnapshot}, nil, nil)

			var progressText string
			mu.Lock()
			completed[subtask.ID] = subtask.Status == SubTaskDone
			completedCnt++
			delete(pending, subtask.ID)
			progressText = fmt.Sprintf("[ACT] Executing %d/%d: %s", completedCnt, totalSeen, subtask.Description)
			mu.Unlock()

			select {
			case <-ctx.Done():
				return
			case obsCh <- &obs:
			}

			select {
			case <-ctx.Done():
				return
			case streamOut <- progressText:
			default:
			}

			mu.Lock()
			scheduleReady()
			mu.Unlock()
		}(st)
	}

	scheduleReady = func() {
		for id, st := range pending {
			if st.Status != SubTaskPending {
				continue
			}
			ready := true
			for _, dep := range st.DependsOn {
				if !completed[dep] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			delete(pending, id)
			startTask(st)
		}
	}

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case st, ok := <-subtaskCh:
			if !ok {
				wg.Wait()
				return nil
			}
			if st == nil {
				continue
			}

			mu.Lock()
			totalSeen++
			if st.Status == "" {
				st.Status = SubTaskPending
			}
			pending[st.ID] = st
			allTasks = append(allTasks, st)
			scheduleReady()
			mu.Unlock()
		}
	}
}
