package heart

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fsEventCollector struct {
	mu     sync.Mutex
	events []Event
	notify chan struct{}
}

func newFSEventCollector() *fsEventCollector {
	return &fsEventCollector{notify: make(chan struct{}, 32)}
}

func (c *fsEventCollector) emit(ev Event) error {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
	return nil
}

func (c *fsEventCollector) find(match func(Event) bool) (Event, bool) {
	return c.findFrom(0, match)
}

func (c *fsEventCollector) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

func (c *fsEventCollector) findFrom(start int, match func(Event) bool) (Event, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ev := range c.events[start:] {
		if match(ev) {
			return ev, true
		}
	}
	return Event{}, false
}

func TestFSSourceEmitsCreateAndModifyEvents(t *testing.T) {
	dir := t.TempDir()
	source := &FSSource{Dirs: []string{dir}}
	collector := newFSEventCollector()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- source.Run(ctx, collector.emit) }()

	var created Event
	filePath := triggerUntilEvent(t, collector, func(attempt int) string {
		path := filepath.Join(dir, fmt.Sprintf("note-%d.txt", attempt))
		if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
			t.Fatalf("write create candidate: %v", err)
		}
		return path
	}, func(ev Event) bool {
		return (ev.Kind == "fs.created" || ev.Kind == "fs.modified") && payloadPath(ev) != ""
	}, 2*time.Second, "create or modify event")
	created, ok := collector.find(func(ev Event) bool {
		return (ev.Kind == "fs.created" || ev.Kind == "fs.modified") && payloadPath(ev) == filePath
	})
	if !ok {
		t.Fatalf("created/modified event for %s not collected", filePath)
	}
	assertFSEventShape(t, created)

	modified := waitForEventAfter(t, collector, func() {
		if err := os.WriteFile(filePath, []byte("second"), 0o644); err != nil {
			t.Fatalf("modify file: %v", err)
		}
	}, func(ev Event) bool {
		return ev.Kind == "fs.modified" && payloadPath(ev) == filePath
	}, 2*time.Second, "modified event")
	assertFSEventShape(t, modified)

	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run returned %v, want %v", err, context.Canceled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancel")
	}
}

func TestFSSourceEmptyDirsReturnsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- (&FSSource{}).Run(ctx, func(Event) error { return nil })
	}()

	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Fatalf("Run returned %v, want %v", err, context.Canceled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly for cancelled empty source")
	}
}

func triggerUntilEvent(t *testing.T, collector *fsEventCollector, action func(attempt int) string, match func(Event) bool, timeout time.Duration, label string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	attempt := 0
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		attempt++
		_ = action(attempt)
		if ev, ok := collector.find(match); ok {
			return payloadPath(ev)
		}
		select {
		case <-collector.notify:
			if ev, ok := collector.find(match); ok {
				return payloadPath(ev)
			}
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", label)
		}
	}
}

func waitForEventAfter(t *testing.T, collector *fsEventCollector, action func(), match func(Event) bool, timeout time.Duration, label string) Event {
	t.Helper()
	start := collector.len()
	action()
	deadline := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if ev, ok := collector.findFrom(start, match); ok {
			return ev
		}
		select {
		case <-collector.notify:
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", label)
		}
	}
}

func assertFSEventShape(t *testing.T, ev Event) {
	t.Helper()
	path := payloadPath(ev)
	if path == "" {
		t.Fatalf("payload path missing: %#v", ev)
	}
	if payloadOp(ev) == "" {
		t.Fatalf("payload op missing: %#v", ev)
	}
	parts := strings.Split(ev.DedupKey, "|")
	if len(parts) != 3 {
		t.Fatalf("dedup key = %q, want path|kind|second", ev.DedupKey)
	}
	if parts[0] != path {
		t.Fatalf("dedup key path = %q, want %q", parts[0], path)
	}
	if parts[1] != ev.Kind {
		t.Fatalf("dedup key kind = %q, want %q", parts[1], ev.Kind)
	}
	if _, err := strconv.ParseInt(parts[2], 10, 64); err != nil {
		t.Fatalf("dedup key second = %q: %v", parts[2], err)
	}
}

func payloadPath(ev Event) string {
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(ev.Payload), &payload); err != nil {
		return ""
	}
	return payload.Path
}

func payloadOp(ev Event) string {
	var payload struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal([]byte(ev.Payload), &payload); err != nil {
		return ""
	}
	return payload.Op
}
