package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Forest-Isle/daimon/internal/agent"
)

type ReplayRecord struct {
	TS      string          `json:"ts"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type ReplayRecorder struct {
	mu          sync.Mutex
	dir         string
	file        *os.File
	currentDate string
	now         func() time.Time
}

func NewReplayRecorder(dir string) (*ReplayRecorder, error) {
	if dir == "" {
		return nil, fmt.Errorf("replay dir is required")
	}
	return &ReplayRecorder{
		dir: dir,
		now: time.Now,
	}, nil
}

func (r *ReplayRecorder) Subscribe(bus agent.EventBus) agent.Subscription {
	if r == nil || bus == nil {
		return nil
	}
	return bus.Subscribe(func(event agent.Event) {
		_ = r.Record(event)
	})
}

func (r *ReplayRecorder) Record(event agent.Event) error {
	if r == nil || event == nil || !isReplayEvent(event) {
		return nil
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal replay payload: %w", err)
	}

	now := r.currentTime().UTC()
	record := ReplayRecord{
		TS:      now.Format(time.RFC3339Nano),
		Type:    event.EventType(),
		Payload: payload,
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal replay record: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.openForDateLocked(now.Format("2006-01-02")); err != nil {
		return err
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write replay event: %w", err)
	}
	return nil
}

func (r *ReplayRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

func (r *ReplayRecorder) currentTime() time.Time {
	if r.now == nil {
		return time.Now()
	}
	return r.now()
}

func (r *ReplayRecorder) openForDateLocked(date string) error {
	if r.file != nil && r.currentDate == date {
		return nil
	}
	if r.file != nil {
		if err := r.file.Close(); err != nil {
			return fmt.Errorf("close replay file: %w", err)
		}
		r.file = nil
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return fmt.Errorf("create replay dir: %w", err)
	}
	path := filepath.Join(r.dir, date+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open replay file: %w", err)
	}
	r.file = file
	r.currentDate = date
	return nil
}

func isReplayEvent(event agent.Event) bool {
	switch event.(type) {
	case agent.ProviderExchange, agent.ToolRoundTrip, agent.TurnClosed:
		return true
	default:
		return false
	}
}
