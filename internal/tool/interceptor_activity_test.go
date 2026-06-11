package tool

import (
	"context"
	"testing"
)

type recordingReporter struct {
	events []bool // one entry per ReportToolActivity call: the done flag
	names  []string
}

func (r *recordingReporter) ReportToolActivity(_ context.Context, call *ToolCall, done bool) {
	r.events = append(r.events, done)
	r.names = append(r.names, call.ToolName)
}

func TestActivityInterceptor_ReportsStartThenFinish(t *testing.T) {
	rep := &recordingReporter{}
	ai := NewActivityInterceptor(rep)

	called := false
	final := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		called = true
		// At the moment the tool runs, exactly one (start) event must precede it.
		if len(rep.events) != 1 || rep.events[0] != false {
			t.Errorf("expected one start event before execution, got %v", rep.events)
		}
		return &ToolResult{Output: "ok"}, nil
	}

	res, err := ai.Intercept(context.Background(), &ToolCall{ToolName: "bash"}, final)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("next() was not invoked")
	}
	if res == nil || res.Output != "ok" {
		t.Fatalf("result not passed through: %+v", res)
	}
	if len(rep.events) != 2 || rep.events[0] != false || rep.events[1] != true {
		t.Errorf("expected [start=false, finish=true], got %v", rep.events)
	}
	for _, n := range rep.names {
		if n != "bash" {
			t.Errorf("reporter saw wrong tool name: %q", n)
		}
	}
}

func TestActivityInterceptor_NilReporterIsTransparent(t *testing.T) {
	ai := NewActivityInterceptor(nil)
	final := func(_ context.Context, _ *ToolCall) (*ToolResult, error) {
		return &ToolResult{Output: "ok"}, nil
	}
	res, err := ai.Intercept(context.Background(), &ToolCall{ToolName: "x"}, final)
	if err != nil || res == nil || res.Output != "ok" {
		t.Fatalf("nil reporter should pass through cleanly, got res=%+v err=%v", res, err)
	}
}
