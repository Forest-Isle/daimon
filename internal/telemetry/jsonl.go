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

type EventRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type JSONLExporter struct {
	mu   sync.Mutex
	file *os.File
}

func NewJSONLExporter(path string) (*JSONLExporter, error) {
	if path == "" {
		return nil, fmt.Errorf("telemetry trace path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create trace dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open trace file: %w", err)
	}
	return &JSONLExporter{file: file}, nil
}

func (e *JSONLExporter) Subscribe(bus agent.EventBus) agent.Subscription {
	if e == nil || bus == nil {
		return nil
	}
	return bus.Subscribe(func(event agent.Event) {
		_ = e.Record(event)
	})
}

func (e *JSONLExporter) Record(event agent.Event) error {
	if e == nil || e.file == nil || event == nil {
		return nil
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}
	record := EventRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Type:      event.EventType(),
		Payload:   payload,
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal event record: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write trace event: %w", err)
	}
	return nil
}

func (e *JSONLExporter) Close() error {
	if e == nil || e.file == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.file.Close()
	e.file = nil
	return err
}
