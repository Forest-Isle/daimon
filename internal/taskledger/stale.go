package taskledger

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// StaleCallback is invoked for each task detected as stale.
type StaleCallback func(task Task)

// StaleDetector periodically checks for tasks with stale heartbeats
// and marks them as failed.
type StaleDetector struct {
	ledger   TaskLedger
	timeout  time.Duration
	interval time.Duration
	onStale  StaleCallback
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewStaleDetector creates a StaleDetector that polls the ledger every interval
// for tasks whose heartbeat is older than timeout. Each stale task is marked
// failed and passed to onStale (if non-nil).
func NewStaleDetector(ledger TaskLedger, timeout, interval time.Duration, onStale StaleCallback) *StaleDetector {
	return &StaleDetector{
		ledger:   ledger,
		timeout:  timeout,
		interval: interval,
		onStale:  onStale,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the background detection goroutine.
func (sd *StaleDetector) Start() {
	sd.wg.Add(1)
	go sd.loop()
}

// Stop signals the background goroutine to exit and waits for it to finish.
func (sd *StaleDetector) Stop() {
	close(sd.stopCh)
	sd.wg.Wait()
}

func (sd *StaleDetector) loop() {
	defer sd.wg.Done()

	ticker := time.NewTicker(sd.interval)
	defer ticker.Stop()

	for {
		select {
		case <-sd.stopCh:
			return
		case <-ticker.C:
			sd.sweep()
		}
	}
}

func (sd *StaleDetector) sweep() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stale, err := sd.ledger.DetectStale(ctx, sd.timeout)
	if err != nil {
		slog.Warn("stale-detector: detect failed", "err", err)
		return
	}

	for _, task := range stale {
		now := time.Now()
		task.State = TaskStateFailed
		task.Result = "stale heartbeat"
		task.CompletedAt = &now
		if err := sd.ledger.Update(ctx, task); err != nil {
			slog.Warn("stale-detector: failed to mark task stale",
				"task_id", task.ID, "err", err)
			continue
		}
		slog.Info("stale-detector: marked task as failed",
			"task_id", task.ID, "title", task.Title)
		if sd.onStale != nil {
			sd.onStale(task)
		}
	}
}
