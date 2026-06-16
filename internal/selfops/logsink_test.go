package selfops

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestErrorRingAppendSnapshotAndOverflow(t *testing.T) {
	ring := NewErrorRing(3)
	ring.Append("one")
	ring.Append("two")
	if got := ring.Snapshot(); !equalStrings(got, []string{"one", "two"}) {
		t.Fatalf("snapshot = %#v, want [one two]", got)
	}

	ring.Append("three")
	ring.Append("four")
	if got := ring.Snapshot(); !equalStrings(got, []string{"two", "three", "four"}) {
		t.Fatalf("overflow snapshot = %#v, want [two three four]", got)
	}
}

func TestErrorRingSnapshotReturnsCopy(t *testing.T) {
	ring := NewErrorRing(2)
	ring.Append("one")
	got := ring.Snapshot()
	got[0] = "changed"
	if again := ring.Snapshot(); !equalStrings(again, []string{"one"}) {
		t.Fatalf("snapshot mutated ring: %#v", again)
	}
}

func TestErrorRingConcurrentAppendSnapshot(t *testing.T) {
	ring := NewErrorRing(16)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				ring.Append(fmt.Sprintf("%d/%d", id, j))
				_ = ring.Snapshot()
			}
		}(i)
	}
	wg.Wait()
	if got := ring.Snapshot(); len(got) > 16 {
		t.Fatalf("snapshot len = %d, want <= 16", len(got))
	}
}

func TestTeeHandlerCapturesErrorsAndDelegates(t *testing.T) {
	var buf bytes.Buffer
	ring := NewErrorRing(8)
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(&teeHandler{base: base, ring: ring}).WithGroup("grp").With("key", "value")

	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	if got := ring.Snapshot(); !equalStrings(got, []string{"error message"}) {
		t.Fatalf("captured errors = %#v, want [error message]", got)
	}
	out := buf.String()
	for _, want := range []string{"msg=\"info message\"", "msg=\"warn message\"", "msg=\"error message\"", "grp.key=value"} {
		if !strings.Contains(out, want) {
			t.Fatalf("delegated output %q does not contain %q", out, want)
		}
	}
}

func TestTeeHandlerEnabledDelegates(t *testing.T) {
	var buf bytes.Buffer
	h := &teeHandler{
		base: slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}),
		ring: NewErrorRing(1),
	}
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info should be disabled by delegated base handler")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("error should be enabled by delegated base handler")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
